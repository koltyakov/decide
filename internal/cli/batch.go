package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
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

type batchJob struct {
	index  int
	state  []byte
	result chan batchResult
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
	cmd.Flags().StringVar(&onlyFailed, "only-failed", "", "prior batch JSONL output file; rerun its selected failures and report indices missing from --states")
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
		var selected map[int]bool
		if onlyFailed != "" {
			if selected, err = failedIndices(onlyFailed, fromIndex); err != nil {
				return err
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
		ctx, cancel := context.WithCancel(cmd.Context())
		closeReader := sync.OnceFunc(func() { _ = reader.Close() })
		stopClose := context.AfterFunc(ctx, closeReader)
		defer stopClose()
		var wg sync.WaitGroup
		defer func() {
			cancel()
			closeReader()
			wg.Wait()
		}()

		work := make(chan batchJob)
		for range jobs {
			wg.Go(func() {
				for job := range work {
					if ctx.Err() != nil {
						close(job.result)
						continue
					}
					job.result <- batchRecord(ctx, client, recipe, policies, mode, job.index, job.state)
				}
			})
		}
		// The writer holds one result channel; the queue holds the rest of the window.
		ordered := make(chan chan batchResult, 2*jobs-1)
		var inputErr error
		wg.Go(func() {
			defer close(ordered)
			defer close(work)
			inputErr = queueBatch(ctx, reader, fromIndex, selected, work, ordered)
		})
		encoder := json.NewEncoder(a.out)
		encoder.SetEscapeHTML(false)
		processed, failures, rejected := 0, 0, false
		for result := range ordered {
			row, started := <-result
			if !started {
				continue
			}
			if err = encoder.Encode(row); err != nil {
				return err
			}
			processed++
			if !row.OK {
				failures++
			}
			if row.Code == fault.Rejected {
				rejected = true
			}
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if inputErr != nil {
			return inputErr
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

func queueBatch(ctx context.Context, reader io.Reader, fromIndex int, selected map[int]bool, work chan<- batchJob, ordered chan<- chan batchResult) error {
	reader, err := batchStateReader(ctx, reader)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), jev.MaxBytes+1)
	index := 0
	for ctx.Err() == nil && (selected == nil || len(selected) > 0) && scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		i := index
		index++
		if i < fromIndex || (selected != nil && !selected[i]) {
			continue
		}
		// Reserve output space before dispatch. A worker can finish even if the
		// writer is waiting for an earlier row or has returned a write error.
		result := make(chan batchResult, 1)
		select {
		case ordered <- result:
		case <-ctx.Done():
			return ctx.Err()
		}
		work <- batchJob{index: i, state: bytes.Clone(scanner.Bytes()), result: result}
		delete(selected, i)
	}
	if scanner.Err() != nil {
		return fault.New(fault.Usage, "cannot read JSONL states; each line must be at most 32 MiB")
	}
	if len(selected) > 0 {
		return fault.New(fault.Usage, fmt.Sprintf("states input is missing --only-failed indices %v", slices.Sorted(maps.Keys(selected))))
	}
	return nil
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
func failedIndices(path string, fromIndex int) (map[int]bool, error) {
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
		if !*row.OK && *row.Index >= fromIndex {
			failed[*row.Index] = true
		}
	}
}
