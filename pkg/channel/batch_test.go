package channel

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var partitionByFirstLetter = func(s string) (string, error) {
	return string(s[0]), nil
}

var partitionErrorOnB = func(s string) (string, error) {
	if s[0] == 'b' {
		return "", assert.AnError
	}
	return string(s[0]), nil
}

func TestBatch(t *testing.T) {
	type testCase[T any] struct {
		name           string
		in             []T
		pauseEveryItem time.Duration
		opts           BatchOptions[T]
		expected       [][]T
		expectedErr    []error
	}
	tests := []testCase[string]{
		{
			name:     "no items",
			in:       []string{},
			opts:     BatchOptions[string]{},
			expected: [][]string{},
		},
		{
			name: "single batch",
			in:   []string{"a", "b", "c"},
			opts: BatchOptions[string]{MaxSize: 3},
			expected: [][]string{
				{"a", "b", "c"},
			},
		},
		{
			name: "multiple batches",
			in:   []string{"a", "b", "c", "d", "e", "f"},
			opts: BatchOptions[string]{MaxSize: 3},
			expected: [][]string{
				{"a", "b", "c"},
				{"d", "e", "f"},
			},
		},
		{
			name: "partition by first letter",
			in:   []string{"aa", "ab", "ba", "bb"},
			opts: BatchOptions[string]{MaxSize: 3, PartitionBy: partitionByFirstLetter},
			expected: [][]string{
				{"aa", "ab"},
				{"ba", "bb"},
			},
		},
		{
			name:           "multiple batches with pause",
			in:             []string{"a", "b", "c", "d", "e", "f"},
			opts:           BatchOptions[string]{MaxSize: 6, MaxWait: 30 * time.Millisecond},
			pauseEveryItem: 10 * time.Millisecond,
			expected: [][]string{
				{"a", "b", "c"},
				{"d", "e", "f"},
			},
		},
		{
			name:           "multiple batches with pause and partition",
			in:             []string{"aa", "ab", "ba", "bb", "ca", "cb"},
			opts:           BatchOptions[string]{MaxSize: 6, MaxWait: 30 * time.Millisecond, PartitionBy: partitionByFirstLetter},
			pauseEveryItem: 10 * time.Millisecond,
			expected: [][]string{
				{"aa", "ab"},
				{"ba", "bb"},
				{"ca", "cb"},
			},
		},
		{
			name: "error on partition",
			in:   []string{"a", "b", "c"},
			opts: BatchOptions[string]{MaxSize: 3, PartitionBy: func(s string) (string, error) {
				return "", assert.AnError
			}},
			expectedErr: []error{assert.AnError, assert.AnError, assert.AnError},
		},
		{
			name: "error on partition after some items",
			in:   []string{"aa", "ab", "ba", "bb"},
			opts: BatchOptions[string]{MaxSize: 4, PartitionBy: partitionErrorOnB},
			expected: [][]string{
				{"aa", "ab"},
			},
			expectedErr: []error{assert.AnError, assert.AnError},
		},
	}

	t.Parallel()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := make(chan string, len(tt.in))
			go func() {
				for _, item := range tt.in {
					in <- item
					time.Sleep(tt.pauseEveryItem)
				}
				close(in)
			}()

			out, errs := Batch(in, tt.opts)

			actual := make([][]string, 0)
			actualErrs := make([]error, 0)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				for batch := range out {
					actual = append(actual, batch)
				}
				wg.Done()
			}()
			go func() {
				for err := range errs {
					actualErrs = append(actualErrs, err)
				}
				wg.Done()
			}()
			wg.Wait()

			assert.ElementsMatch(t, tt.expected, actual)
			assert.ElementsMatch(t, tt.expectedErr, actualErrs)
		})
	}
}
