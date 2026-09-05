package middleware

import "net/http"

func Chain(handlers ...func(http.Handler) http.Handler) func(handler http.Handler) http.Handler {
	return func(final http.Handler) http.Handler {
		for i := len(handlers) - 1; i >= 0; i-- {
			final = handlers[i](final)
		}
		return final
	}
}
