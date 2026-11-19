package fofaworld

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const maxLineBytes = 64 << 10

type event struct {
	kind   string
	asset  Asset
	result Result
	total  int
	count  int
	err    error
}

// lineWriter bounds memory independently of subprocess write boundaries.
type lineWriter struct {
	buffer []byte
	line   func([]byte) error
	err    error
	stop   func()
}

func (w *lineWriter) Write(p []byte) (int, error) {
	defer func() {
		if w.err != nil && w.stop != nil {
			w.stop()
		}
	}()
	if w.err != nil {
		return 0, w.err
	}
	n := len(p)
	for len(p) > 0 {
		index := bytes.IndexByte(p, '\n')
		end := len(p)
		if index >= 0 {
			end = index
		}
		if len(w.buffer)+end > maxLineBytes {
			w.err = failure("output_limit")
			return 0, w.err
		}
		w.buffer = append(w.buffer, p[:end]...)
		if index < 0 {
			break
		}
		w.err = w.line(bytes.TrimSuffix(w.buffer, []byte{'\r'}))
		w.buffer = w.buffer[:0]
		if w.err != nil {
			return 0, w.err
		}
		p = p[index+1:]
	}
	return n, nil
}

func (w *lineWriter) finish() error {
	if w.err == nil && len(w.buffer) > 0 {
		w.err = w.line(w.buffer)
		w.buffer = nil
	}
	return w.err
}

var logPrefix = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} `)
var progressLine = regexp.MustCompile(`^size: (\d+)/(\d+), [-+A-Za-z0-9.]+%$`)

func collect(ctx context.Context, input Input, events chan<- event) {
	commandCtx, cancelCommand := context.WithCancel(ctx)
	defer cancelCommand()
	emit := func(e event) error {
		select {
		case events <- e:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	out := &lineWriter{stop: cancelCommand, line: func(line []byte) error {
		if len(bytes.TrimSpace(line)) == 0 {
			return nil
		}
		asset, err := decodeAsset(line)
		if err != nil {
			return err
		}
		return emit(event{kind: "asset", asset: asset})
	}}
	stderr := &lineWriter{stop: cancelCommand, line: func(line []byte) error {
		text := logPrefix.ReplaceAllString(string(line), "")
		if strings.HasPrefix(text, "fetch error:") {
			return emit(event{kind: "source_error", err: failure("collection_failed")})
		}
		// Newer clients repeat the query after progress. Another query's log
		// cannot establish completeness for the rows this request collected.
		text = strings.TrimSuffix(text, " for query: "+input.Query)
		if match := progressLine.FindStringSubmatch(text); match != nil {
			count, countErr := strconv.Atoi(match[1])
			total, totalErr := strconv.Atoi(match[2])
			if countErr != nil || totalErr != nil {
				return failure("invalid_progress")
			}
			return emit(event{kind: "progress", count: count, total: total})
		}
		if text != "" && !strings.HasPrefix(text, "dump data of query:") {
			return emit(event{kind: "source_warning"})
		}
		return nil
	}}
	args := []string{"dump", "--format=json", "--batchSize", strconv.Itoa(min(input.BatchSize, input.MaxAssets)),
		"--size", strconv.Itoa(input.MaxAssets), "--fields", "host,ip,port,protocol,country,region,city,longitude,latitude", "--", input.Query}
	cmd := exec.CommandContext(commandCtx, input.ClientPath, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = input.Workspace, out, stderr
	err := runCommand(commandCtx, cmd)
	// Cmd.Wait joins both copy writers before their state is inspected.
	parseErr := errors.Join(out.finish(), stderr.finish())
	if parseErr != nil {
		err = parseErr
	} else if err != nil {
		err = failure("client_failed")
	}
	// The reducer owns this channel until command and workers have joined, so
	// this final receipt must be delivered even after cancellation.
	events <- event{kind: "collection_done", err: err}
}
