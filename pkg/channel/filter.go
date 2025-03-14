package channel

func Filter[T any](in chan T, fn func(T) bool) chan T {
	out := make(chan T)
	go func() {
		defer close(out)
		for item := range in {
			if fn(item) {
				out <- item
			}
		}
	}()
	return out
}
