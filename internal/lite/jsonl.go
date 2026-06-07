package lite

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	directoryPerm = 0o750
	filePerm      = 0o640
)

type jsonlWriter struct {
	path        string
	maxBytes    int64
	maxBackups  int
	file        *os.File
	writer      *bufio.Writer
	currentSize int64
}

func newJSONLWriter(path string, maxBytes int64, maxBackups int) (*jsonlWriter, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("max bytes must be greater than 0")
	}
	if maxBackups < 1 {
		return nil, fmt.Errorf("max backups must be at least 1")
	}
	if err := os.MkdirAll(filepath.Dir(path), directoryPerm); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	writer := &jsonlWriter{
		path:       path,
		maxBytes:   maxBytes,
		maxBackups: maxBackups,
	}
	if err := writer.open(); err != nil {
		return nil, err
	}

	return writer, nil
}

func (w *jsonlWriter) appendLine(line []byte) error {
	if w.file == nil {
		return fmt.Errorf("jsonl writer is closed")
	}

	lineLength := int64(len(line) + 1)
	if w.currentSize > 0 && w.currentSize+lineLength > w.maxBytes {
		if err := w.rotate(); err != nil {
			return err
		}
	}

	if _, err := w.writer.Write(line); err != nil {
		return fmt.Errorf("write jsonl record: %w", err)
	}
	if err := w.writer.WriteByte('\n'); err != nil {
		return fmt.Errorf("write jsonl newline: %w", err)
	}
	if err := w.writer.Flush(); err != nil {
		return fmt.Errorf("flush jsonl record: %w", err)
	}

	w.currentSize += lineLength
	return nil
}

func (w *jsonlWriter) close() error {
	if w.file == nil {
		return nil
	}

	var flushErr error
	if w.writer != nil {
		flushErr = w.writer.Flush()
	}
	closeErr := w.file.Close()
	w.file = nil
	w.writer = nil
	if flushErr != nil {
		return flushErr
	}

	return closeErr
}

func (w *jsonlWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("open jsonl file: %w", err)
	}
	if err := file.Chmod(filePerm); err != nil {
		file.Close()
		return fmt.Errorf("set jsonl file permissions: %w", err)
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("stat jsonl file: %w", err)
	}

	w.file = file
	w.writer = bufio.NewWriter(file)
	w.currentSize = stat.Size()
	return nil
}

func (w *jsonlWriter) rotate() error {
	if err := w.close(); err != nil {
		return fmt.Errorf("close jsonl before rotate: %w", err)
	}

	oldest := fmt.Sprintf("%s.%d", w.path, w.maxBackups)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove oldest jsonl backup: %w", err)
	}

	for index := w.maxBackups - 1; index >= 1; index-- {
		from := fmt.Sprintf("%s.%d", w.path, index)
		to := fmt.Sprintf("%s.%d", w.path, index+1)
		if err := os.Rename(from, to); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rename jsonl backup: %w", err)
		}
	}

	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rotate jsonl file: %w", err)
	}

	return w.open()
}

func appendJSONField(line *[]byte, name string, value string, first bool) {
	if !first {
		*line = append(*line, ',')
	}
	appendJSONString(line, name)
	*line = append(*line, ':')
	appendJSONString(line, value)
}

func appendJSONIntField(line *[]byte, name string, value int, first bool) {
	if !first {
		*line = append(*line, ',')
	}
	appendJSONString(line, name)
	*line = append(*line, ':')
	*line = fmt.Appendf(*line, "%d", value)
}

func appendJSONFloatField(line *[]byte, name string, value float64, first bool) {
	if !first {
		*line = append(*line, ',')
	}
	appendJSONString(line, name)
	*line = append(*line, ':')
	*line = fmt.Appendf(*line, "%g", value)
}

func appendJSONString(line *[]byte, value string) {
	encoded, _ := json.Marshal(value)
	*line = append(*line, encoded...)
}
