package llm

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

type serverSentEvent struct {
	Event string
	Data  string
}

func scanServerSentEvents(ctx context.Context, r io.Reader, handle func(serverSentEvent) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16<<20)

	var eventName string
	var dataLines []string
	flush := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		event := serverSentEvent{
			Event: strings.TrimSpace(eventName),
			Data:  strings.Join(dataLines, "\n"),
		}
		eventName = ""
		dataLines = nil
		return handle(event)
	}

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if ok && strings.HasPrefix(value, " ") {
			value = strings.TrimPrefix(value, " ")
		}
		if !ok {
			field = line
			value = ""
		}
		switch field {
		case "event":
			eventName = value
		case "data":
			dataLines = append(dataLines, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read event stream: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return flush()
}
