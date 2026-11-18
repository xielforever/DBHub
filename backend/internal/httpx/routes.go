package httpx

import "net/http"

// Both ServeMux and the audited registrar implement this interface.
type RouteRegistrar interface {
	Handle(string, http.Handler)
	HandleFunc(string, func(http.ResponseWriter, *http.Request))
}
