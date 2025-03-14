package format

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJSONEachRowReader_Read_SingleValue(t *testing.T) {
	// Create a reader with a single value
	r := NewJSONEachRowReader([]any{
		map[string]string{"key": "value"},
	})

	// Read into a buffer
	var buf bytes.Buffer
	_, err := io.Copy(&buf, r)
	assert.NoError(t, err)

	// Verify the output
	assert.Equal(t, `{"key":"value"}`, buf.String())
}

func TestJSONEachRowReader_Read_MultipleValues(t *testing.T) {
	// Create a reader with multiple values
	r := NewJSONEachRowReader([]any{
		map[string]string{"key1": "value1"},
		map[string]string{"key2": "value2"},
		map[string]string{"key3": "value3"},
	})

	// Read into a buffer
	var buf bytes.Buffer
	_, err := io.Copy(&buf, r)
	assert.NoError(t, err)

	// Verify the output has correct newline separators
	expected := `{"key1":"value1"}
{"key2":"value2"}
{"key3":"value3"}`
	assert.Equal(t, expected, buf.String())
}

func TestJSONEachRowReader_Read_EmptyReader(t *testing.T) {
	// Create an empty reader
	r := NewJSONEachRowReader([]any{})

	// Read into a buffer
	buf := make([]byte, 10)
	n, err := r.Read(buf)

	// Should get EOF and zero bytes read
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)
}

func TestJSONEachRowReader_Read_SmallBuffer(t *testing.T) {
	// Create a reader with values that will be larger than our read buffer
	r := NewJSONEachRowReader([]any{
		map[string]string{"key1": "value1"},
		map[string]string{"key2": "value2"},
	})

	// Use a very small buffer to force multiple reads
	var buf bytes.Buffer
	smallBuf := make([]byte, 5) // Smaller than a single value

	// Manually read in chunks to simulate small buffers
	// Add a safety counter to prevent infinite loops
	maxIterations := 100
	iterations := 0

	for iterations < maxIterations {
		iterations++
		n, err := r.Read(smallBuf)
		if n > 0 {
			buf.Write(smallBuf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
	}

	assert.Less(t, iterations, maxIterations, "Too many iterations, possible infinite loop")

	// Verify the output is still correct
	expected := `{"key1":"value1"}
{"key2":"value2"}`
	assert.Equal(t, expected, buf.String())
}

func TestJSONEachRowReader_Read_Add(t *testing.T) {
	// Create an empty reader
	r := NewJSONEachRowReader(nil)

	// Add values incrementally
	r.Add(map[string]string{"key1": "value1"})
	r.Add(map[string]string{"key2": "value2"})

	// Read into a buffer
	var buf bytes.Buffer
	_, err := io.Copy(&buf, r)
	assert.NoError(t, err)

	// Verify the output
	expected := `{"key1":"value1"}
{"key2":"value2"}`
	assert.Equal(t, expected, buf.String())
}

func TestJSONEachRowReader_Read_LargeValue(t *testing.T) {
	// Create a value with a large string
	largeString := strings.Repeat("a", 1000)
	r := NewJSONEachRowReader([]any{
		map[string]string{"large": largeString},
	})

	// Use a small buffer to force multiple reads
	var buf bytes.Buffer
	smallBuf := make([]byte, 100) // Smaller than the value

	// Manually read in chunks
	totalBytes := 0
	for {
		n, err := r.Read(smallBuf)
		totalBytes += n
		buf.Write(smallBuf[:n])
		if err == io.EOF {
			break
		}
		assert.NoError(t, err, "Unexpected error during read")
	}

	// Verify we read more than one chunk
	assert.Greater(t, totalBytes, 100, "Should have read multiple chunks")

	// Verify the output is correct
	expected := `{"large":"` + largeString + `"}`
	assert.Equal(t, expected, buf.String())
}

func TestJSONEachRowReader_Read_ComplexValues(t *testing.T) {
	// Create a reader with complex nested values
	r := NewJSONEachRowReader([]any{
		map[string]any{
			"nested": map[string]any{
				"array": []int{1, 2, 3},
				"object": map[string]string{
					"inner": "value",
				},
			},
		},
		[]any{1, "string", true, nil},
	})

	// Read into a buffer
	var buf bytes.Buffer
	_, err := io.Copy(&buf, r)
	assert.NoError(t, err)

	// Verify the output
	expected := `{"nested":{"array":[1,2,3],"object":{"inner":"value"}}}
[1,"string",true,null]`
	assert.Equal(t, expected, buf.String())
}

func TestJSONEachRowReader_Read_ExactBufferSize(t *testing.T) {
	// Create a value that will exactly match our buffer size
	r := NewJSONEachRowReader([]any{
		map[string]string{"key": "value"}, // Length is 15 bytes: {"key":"value"}
	})

	// Use a buffer exactly matching the expected output
	buf := make([]byte, 15) // Exact size of the value

	// Read should fill the buffer exactly
	n, err := r.Read(buf)
	assert.Equal(t, 15, n)
	assert.NoError(t, err)

	// Next read should return EOF
	n, err = r.Read(buf)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)

	// Verify the output
	assert.Equal(t, `{"key":"value"}`, string(buf))
}

func TestJSONEachRowReader_Read_NewlineHandling(t *testing.T) {
	// Test proper newline handling between values
	r := NewJSONEachRowReader([]any{
		map[string]string{"key1": "value1"},
		map[string]string{"key2": "value2"},
	})

	// Let's read the full content
	data, err := io.ReadAll(r)
	assert.NoError(t, err)

	// Verify the output has correct newline separator
	expected := `{"key1":"value1"}
{"key2":"value2"}`
	assert.Equal(t, expected, string(data))

	// A new reader should be empty after reading
	n, err := r.Read(make([]byte, 10))
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)
}
