package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/koltyakov/decide/internal/decision"
	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/spf13/cobra"
)

type batchResult struct {
	Index    int    `json:"index"`
	OK       bool   `json:"ok"`
	Response any    `json:"response,omitempty"`
	Error    string `json:"error,omitempty"`
	Code     int    `json:"code,omitempty"`
}

func closed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// batchSlot is one record in flight or awaiting in-order output.
type batchSlot struct {
	done chan struct{}
	row  batchResult
}

func (a *app) batch() *cobra.Command {
	var states, onlyFailed string
	var jobs, fromIndex int
	var flags executionFlags
	cmd := &cobra.Command{Use: "batch RECIPE", Short: "Evaluate a reusable System One recipe over JSONL states", Args: oneArgument("batch")}
	flags.bind(cmd)
	cmd.Flags().StringVar(&states, "states", "-", "JSONL states file or - for stdin; each line is a native state value")
	cmd.Flags().IntVar(&jobs, "jobs", 1, "maximum records in flight, 1 to 16; output stays in input order")
	cmd.Flags().IntVar(&fromIndex, "from-index", 0, "skip records before this zero-based index, to resume a batch")
	cmd.Flags().StringVar(&onlyFailed, "only-failed", "", "prior batch JSONL output file; rerun only its failed record indices")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if args[0] == "-" {
			return fault.New(fault.Usage, "batch requires a recipe file; stdin is reserved for JSONL states")
		}
		if jobs < 1 || jobs > 16 {
			return fault.New(fault.Usage, "--jobs must be between 1 and 16")
		}
		if fromIndex < 0 {
			return fault.New(fault.Usage, "--from-index must not be negative")
		}
		if onlyFailed == "-" {
			return fault.New(fault.Usage, "--only-failed requires a file; stdin is reserved for JSONL states")
		}
		if flags.output != "json" || a.pretty {
			return fault.New(fault.Usage, "batch writes compact JSONL; --output must be json and --pretty is unsupported")
		}
		request, err := a.recipe(args[0])
		if err != nil {
			return err
		}
		policies, err := flags.policies(cmd)
		if err != nil {
			return err
		}
		client, err := a.client()
		if err != nil {
			return err
		}
		options, err := client.Resolve(a.options)
		if err != nil {
			return err
		}
		// Questions, assets, and provider rules are validated, and images
		// decoded, once. A recipe failure is a usage error, not N record errors.
		recipe, err := jev.NewRecipe(request, options)
		if err != nil {
			return err
		}
		if err = flags.validate(cmd, policies, recipe.Questions()); err != nil {
			return err
		}
		selected, last := map[int]bool(nil), -1
		if onlyFailed != "" {
			if selected, err = failedIndices(onlyFailed); err != nil {
				return err
			}
			for index := range selected {
				last = max(last, index)
			}
		}
		mode := flags.mode()
		if mode == decision.Execute {
			client = client.Preload(recipe)
		}
		reader, err := a.open(states)
		if err != nil {
			return err
		}
		defer func() { _ = reader.Close() }()
		ctx := cmd.Context()
		stopClose := context.AfterFunc(ctx, func() { _ = reader.Close() })
		defer stopClose()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64<<10), jev.MaxBytes+1)
		encoder := json.NewEncoder(a.out)
		encoder.SetEscapeHTML(false)
		var wg sync.WaitGroup
		defer wg.Wait()
		// At most --jobs requests are in flight, and at most twice that many
		// records are held for in-order output. A slow record delays its own
		// output, but later records keep starting until the buffer is full.
		limit := 2 * jobs
		queue := make([]*batchSlot, 0, limit)
		finished := make(chan struct{}, limit)
		index, processed, failures, inFlight, rejected, eof := 0, 0, 0, 0, false, false
		for {
			for !eof && len(queue) < limit && inFlight < jobs && ctx.Err() == nil {
				if selected != nil && index > last {
					eof = true // Every selected record has been read.
					break
				}
				if !scanner.Scan() {
					eof = true
					break
				}
				if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
					continue
				}
				i := index
				index++
				if i < fromIndex || (selected != nil && !selected[i]) {
					continue
				}
				line := bytes.Clone(scanner.Bytes())
				slot := &batchSlot{done: make(chan struct{})}
				queue = append(queue, slot)
				inFlight++
				wg.Go(func() {
					slot.row = batchRecord(ctx, client, recipe, policies, mode, i, line)
					close(slot.done)
					finished <- struct{}{}
				})
			}
			for len(queue) > 0 && closed(queue[0].done) {
				row := queue[0].row
				queue[0] = nil
				queue = queue[1:]
				processed++
				if !row.OK {
					failures++
				}
				if row.Code == fault.Rejected {
					rejected = true
				}
				if err = encoder.Encode(row); err != nil {
					return err
				}
			}
			if len(queue) == 0 && (eof || ctx.Err() != nil) {
				break
			}
			<-finished
			inFlight--
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = scanner.Err(); err != nil {
			return fault.New(fault.Usage, "cannot read JSONL states; each line must be at most 32 MiB")
		}
		if failures > 0 {
			return fault.New(fault.Batch, fmt.Sprintf("batch completed with %d failed records out of %d; requests were not retried", failures, processed))
		}
		if rejected {
			return fault.New(fault.Rejected, "one or more batch decision gates were not met")
		}
		return nil
	}
	return cmd
}

func batchRecord(ctx context.Context, client jev.Client, recipe *jev.Recipe, policies decision.Policies, mode decision.Mode, index int, line []byte) batchResult {
	row := batchResult{Index: index}
	prepared, err := recipe.PrepareJSON(line)
	if err == nil {
		var result *decision.Evaluation
		result, err = decision.Run(ctx, client, prepared, policies, mode)
		if result != nil {
			row.Response = result.Response
			if result.Rejected() {
				row.Code = fault.Rejected
			}
		}
	}
	row.OK = err == nil
	if err != nil {
		row.Error = err.Error()
		row.Code = fault.Code(err)
	}
	return row
}

// failedIndices reads prior batch output and returns the indices of failed
// records. Rejected gates are valid results and are not selected.
func failedIndices(path string) (map[int]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fault.New(fault.Usage, "cannot open --only-failed file")
	}
	defer func() { _ = f.Close() }()
	invalid := fault.New(fault.Usage, "--only-failed must be decide batch JSONL output")
	d := json.NewDecoder(f)
	failed := map[int]bool{}
	for {
		var row struct {
			Index *int  `json:"index"`
			OK    *bool `json:"ok"`
		}
		if err = d.Decode(&row); err == io.EOF {
			return failed, nil
		} else if err != nil || row.Index == nil || row.OK == nil || *row.Index < 0 {
			return nil, invalid
		}
		if !*row.OK {
			failed[*row.Index] = true
		}
	}
}
