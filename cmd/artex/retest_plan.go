package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Autumn-27/artex/retestplan"
)

const maxRetestInputBytes = 4 << 20

// runRetestPlan runs before backend startup: no config, database, LLM, proxy,
// self-update bootstrap or log capture is needed. Only JSON goes to stdout.
func runRetestPlan(filename string, stdin io.Reader, stdout, stderr io.Writer) int {
	inputError := func(err error) int {
		fmt.Fprintf(stderr, "retest-plan: %v\n", err)
		return 2
	}
	if filename == "" {
		return inputError(fmt.Errorf("provide a JSON filename or - for stdin"))
	}
	reader := stdin
	if filename != "-" {
		file, err := os.Open(filename)
		if err != nil {
			return inputError(err)
		}
		defer file.Close()
		reader = file
	}
	input, err := readRetestInput(reader)
	if err != nil {
		return inputError(err)
	}
	plan, err := retestplan.Build(input)
	if err != nil {
		return inputError(err)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(plan); err != nil {
		fmt.Fprintf(stderr, "retest-plan: write output: %v\n", err)
		return 1
	}
	return 0
}

func readRetestInput(reader io.Reader) (retestplan.Input, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxRetestInputBytes+1))
	if err != nil {
		return retestplan.Input{}, err
	}
	if len(raw) > maxRetestInputBytes {
		return retestplan.Input{}, fmt.Errorf("input exceeds 4 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input retestplan.Input
	if err := decoder.Decode(&input); err != nil {
		return retestplan.Input{}, fmt.Errorf("invalid input: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return retestplan.Input{}, fmt.Errorf("input must contain exactly one JSON document")
	}
	return input, nil
}
