package format

import (
	"bytes"
	"io"

	"github.com/goccy/go-json"
)

// JSONEachRowReader is a reader that allows to read JSON objects one by one
// in a format that is compatible with ClickHouse's JSONEachRow format.
type JSONEachRowReader struct {
	values []any        // Values to be marshaled to JSON
	buffer bytes.Buffer // Buffer for the marshaled data
	ready  bool         // Whether the buffer has been prepared
}

// NewJSONEachRowReader creates a new JSONEachRowReader from a slice of values
func NewJSONEachRowReader(values []any) *JSONEachRowReader {
	return &JSONEachRowReader{
		values: values,
	}
}

func (r *JSONEachRowReader) Len() int {
	return len(r.values)
}

// Add adds a value to the reader
func (r *JSONEachRowReader) Add(value any) {
	// If we've already prepared the buffer, we can't add more values
	if r.ready {
		return
	}
	r.values = append(r.values, value)
}

// prepareBuffer marshals all values to JSON and writes them to the buffer
func (r *JSONEachRowReader) prepareBuffer() error {
	if r.ready {
		return nil
	}

	for i, v := range r.values {
		// Add newline separator between values
		if i > 0 {
			r.buffer.WriteByte('\n')
		}

		// Marshal the value to JSON
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return err
		}

		// Write the JSON to the buffer
		r.buffer.Write(jsonBytes)
	}

	r.ready = true
	return nil
}

func (r *JSONEachRowReader) Read(p []byte) (n int, err error) {
	// Prepare the buffer if it hasn't been prepared yet
	if !r.ready {
		if err := r.prepareBuffer(); err != nil {
			return 0, err
		}
	}

	// If the buffer is empty, return EOF
	if r.buffer.Len() == 0 {
		return 0, io.EOF
	}

	// Read from the buffer
	return r.buffer.Read(p)
}
