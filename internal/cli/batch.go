package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
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

func (a *app) batch() *cobra.Command {
	var states string
	var jobs int
	var flags executionFlags
	cmd := &cobra.Command{Use: "batch RECIPE", Short: "Evaluate a reusable System One recipe over JSONL states", Args: oneArgument("batch")}
	flags.bind(cmd)
	cmd.Flags().StringVar(&states, "states", "-", "JSONL states file or - for stdin; each line is a native state value")
	cmd.Flags().IntVar(&jobs, "jobs", 1, "maximum concurrent requests, 1 to 16; ordered JSONL output")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if args[0] == "-" {
			return fault.New(fault.Usage, "batch requires a recipe file; stdin is reserved for JSONL states")
		}
		if jobs < 1 || jobs > 16 {
			return fault.New(fault.Usage, "--jobs must be between 1 and 16")
		}
		if flags.output != "json" || a.pretty {
			return fault.New(fault.Usage, "batch writes compact JSONL; --output must be json and --pretty is unsupported")
		}
		recipe, err := a.recipe(args[0])
		if err != nil {
			return err
		}
		probe := maps.Clone(recipe)
		probe["state"] = ""
		if err = jev.Validate(probe); err != nil {
			return err
		}
		policy := flags.policy(cmd)
		if err = flags.validate(policy, probe["questions"].(map[string]any)); err != nil {
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
		reader, err := a.open(states)
		if err != nil {
			return err
		}
		defer func() { _ = reader.Close() }()
		stopClose := context.AfterFunc(cmd.Context(), func() { _ = reader.Close() })
		defer stopClose()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64<<10), jev.MaxBytes+1)
		encoder := json.NewEncoder(a.out)
		encoder.SetEscapeHTML(false)
		index, failures, rejected := 0, 0, false
		// Fixed-size windows bound both in-flight requests and reordering memory.
		// A slow first row cannot accumulate an unbounded queue of later results.
		for {
			if err = cmd.Context().Err(); err != nil {
				return err
			}
			lines := make([][]byte, 0, jobs)
			for len(lines) < jobs && scanner.Scan() {
				if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
					continue
				}
				lines = append(lines, bytes.Clone(scanner.Bytes()))
			}
			if err = cmd.Context().Err(); err != nil {
				return err
			}
			results := make([]batchResult, len(lines))
			var wg sync.WaitGroup
			for i, line := range lines {
				wg.Add(1)
				go func() {
					defer wg.Done()
					row := batchResult{Index: index + i}
					// The recipe is immutable; Prepare owns the deep copy before encoding.
					request := maps.Clone(recipe)
					var state any
					e := jev.Decode(line, &state)
					if e == nil {
						request["state"] = state
						var result *decision.Evaluation
						result, e = decision.Evaluate(cmd.Context(), client, request, options, policy, flags.dryRun)
						if result != nil {
							row.Response = result.Response
							if result.Rejected() {
								row.Code = fault.Rejected
							}
						}
					}
					row.OK = e == nil
					if e != nil {
						row.Error = e.Error()
						row.Code = fault.Code(e)
					}
					results[i] = row
				}()
			}
			wg.Wait()
			for _, row := range results {
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
			index += len(lines)
			if err = scanner.Err(); err != nil {
				return fault.New(fault.Usage, "cannot read JSONL states; each line must be at most 32 MiB")
			}
			if len(lines) == 0 {
				break
			}
		}
		if failures > 0 {
			return fault.New(fault.Batch, fmt.Sprintf("batch completed with %d failed records out of %d; requests were not retried", failures, index))
		}
		if rejected {
			return fault.New(fault.Rejected, "one or more batch decision gates were not met")
		}
		return nil
	}
	return cmd
}
