package aarv

import (
	"fmt"
	"net/http"
)

// HandlerFunc is the framework's handler signature — every handler converts to this.
type HandlerFunc func(*Context) error

// adaptHandler converts supported handler signatures to HandlerFunc at registration time.
func adaptHandler(handler any) HandlerFunc {
	switch h := handler.(type) {
	case HandlerFunc:
		return h
	case func(*Context) error:
		return HandlerFunc(h)
	case func(http.ResponseWriter, *http.Request):
		return func(c *Context) error {
			h(c.Response(), c.Request())
			return nil
		}
	case http.Handler:
		return func(c *Context) error {
			h.ServeHTTP(c.Response(), c.Request())
			return nil
		}
	case http.HandlerFunc:
		return func(c *Context) error {
			h(c.Response(), c.Request())
			return nil
		}
	default:
		panic(fmt.Sprintf("aarv: unsupported handler type %T", handler))
	}
}
