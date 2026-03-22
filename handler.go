package aarv

import (
	"fmt"
	"net/http"
	"reflect"
	"sync"
)

// HandlerFunc is the framework's handler signature — every handler converts to this.
type HandlerFunc func(*Context) error

type adaptedHandler struct {
	fn         HandlerFunc
	preHandled bool
}

var preHandledHandlerRegistry sync.Map

func registerPreHandledHandler(h HandlerFunc) HandlerFunc {
	preHandledHandlerRegistry.Store(reflect.ValueOf(h).Pointer(), struct{}{})
	return h
}

func isPreHandledHandler(h HandlerFunc) bool {
	_, ok := preHandledHandlerRegistry.Load(reflect.ValueOf(h).Pointer())
	return ok
}

// adaptHandler converts supported handler signatures to HandlerFunc at registration time.
func adaptHandler(handler any) adaptedHandler {
	switch h := handler.(type) {
	case HandlerFunc:
		return adaptedHandler{fn: h, preHandled: isPreHandledHandler(h)}
	case func(*Context) error:
		// Named/unnamed function type conversion preserves the underlying
		// function pointer identity, so the registry lookup still works here.
		hf := HandlerFunc(h)
		return adaptedHandler{fn: hf, preHandled: isPreHandledHandler(hf)}
	case func(http.ResponseWriter, *http.Request):
		return adaptedHandler{fn: func(c *Context) error {
			h(c.Response(), c.Request())
			return nil
		}}
	case http.HandlerFunc:
		return adaptedHandler{fn: func(c *Context) error {
			h(c.Response(), c.Request())
			return nil
		}}
	case http.Handler:
		return adaptedHandler{fn: func(c *Context) error {
			h.ServeHTTP(c.Response(), c.Request())
			return nil
		}}
	default:
		panic(fmt.Sprintf("aarv: unsupported handler type %T", handler))
	}
}
