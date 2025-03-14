package channel

func Buffered[T any](in chan T, bufferSize int) chan T {
	out := make(chan T, bufferSize)
	go func() {
		defer close(out)
		for item := range in {
			out <- item
		}
	}()
	return out
}
