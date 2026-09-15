package proxy

import (
	"io"
	"net/http"

	"golang.org/x/net/http/httpguts"
)

type flushingWriter struct {
	io.Writer
	controller *http.ResponseController
}

func (w *flushingWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil {
		err = w.controller.Flush()
	}
	return n, err
}

func announceTrailers(destination, trailers http.Header) []string {
	var names []string
	for name := range trailers {
		if httpguts.ValidHeaderFieldName(name) && httpguts.ValidTrailerHeader(name) {
			destination.Add("Trailer", name)
			names = append(names, name)
		}
	}
	return names
}

func copyTrailers(destination, trailers http.Header, names []string) {
	for _, name := range names {
		for _, value := range trailers[name] {
			if httpguts.ValidHeaderFieldValue(value) {
				destination.Add(name, value)
			}
		}
	}
}
