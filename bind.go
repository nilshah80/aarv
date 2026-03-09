package aarv

import (
	"net/http"
	"reflect"
)

// Bind creates a typed handler with automatic request parsing and response serialization.
// At registration time, it pre-computes the binder and validator for Req.
// At request time, it decodes the request into Req, validates it, calls the handler,
// and serializes the Res as JSON.
func Bind[Req any, Res any](fn func(*Context, Req) (Res, error)) HandlerFunc {
	// Registration time: pre-compute binder and validator
	var req Req
	reqType := reflect.TypeOf(req)
	binder := buildStructBinder(reqType)
	validator := buildStructValidator(reqType)
	needBody := binder != nil && binder.needBody
	needBinding := binder != nil && len(binder.fields) > 0
	needDefaults := binder != nil && binder.hasDefaults

	if needBody && !needBinding && !needDefaults {
		return func(c *Context) error {
			var req Req

			if c.req.ContentLength > 0 {
				if err := c.BindJSON(&req); err != nil {
					return &BindError{Err: err, Source: "body"}
				}
			}

			if validator != nil {
				if errs := validator.validate(&req); len(errs) > 0 {
					return &ValidationErrors{Errors: errs}
				}
			}

			res, err := fn(c, req)
			if err != nil {
				return err
			}
			if !c.Written() {
				return c.JSON(http.StatusOK, res)
			}
			return nil
		}
	}

	return func(c *Context) error {
		var req Req

		// Step 1: Multi-source binding (param, query, header, cookie, form)
		if needBinding {
			if err := binder.bind(c, &req); err != nil {
				return &BindError{Err: err, Source: "binding"}
			}
		}

		// Step 2: Body parsing (only if struct has json tags and body exists)
		if needBody && c.Request().ContentLength > 0 {
			if err := c.BindJSON(&req); err != nil {
				return &BindError{Err: err, Source: "body"}
			}
		}

		// Step 3: Apply defaults for zero-value fields
		if needDefaults {
			binder.applyDefaults(&req)
		}

		// Step 4: Validation
		if validator != nil {
			if errs := validator.validate(&req); len(errs) > 0 {
				return &ValidationErrors{Errors: errs}
			}
		}

		// Step 5: Call user handler
		res, err := fn(c, req)
		if err != nil {
			return err
		}

		// Step 6: Serialize response
		if !c.Written() {
			return c.JSON(http.StatusOK, res)
		}
		return nil
	}
}

// BindReq creates a handler with request parsing but manual response writing.
func BindReq[Req any](fn func(*Context, Req) error) HandlerFunc {
	var req Req
	reqType := reflect.TypeOf(req)
	binder := buildStructBinder(reqType)
	validator := buildStructValidator(reqType)
	needBody := binder != nil && binder.needBody
	needBinding := binder != nil && len(binder.fields) > 0
	needDefaults := binder != nil && binder.hasDefaults

	if needBody && !needBinding && !needDefaults {
		return func(c *Context) error {
			var req Req

			if c.req.ContentLength > 0 {
				if err := c.BindJSON(&req); err != nil {
					return &BindError{Err: err, Source: "body"}
				}
			}

			if validator != nil {
				if errs := validator.validate(&req); len(errs) > 0 {
					return &ValidationErrors{Errors: errs}
				}
			}

			return fn(c, req)
		}
	}

	return func(c *Context) error {
		var req Req

		if needBinding {
			if err := binder.bind(c, &req); err != nil {
				return &BindError{Err: err, Source: "binding"}
			}
		}

		if needBody && c.Request().ContentLength > 0 {
			if err := c.BindJSON(&req); err != nil {
				return &BindError{Err: err, Source: "body"}
			}
		}

		if needDefaults {
			binder.applyDefaults(&req)
		}

		if validator != nil {
			if errs := validator.validate(&req); len(errs) > 0 {
				return &ValidationErrors{Errors: errs}
			}
		}

		return fn(c, req)
	}
}

// BindRes creates a handler with automatic response serialization but no request parsing.
func BindRes[Res any](fn func(*Context) (Res, error)) HandlerFunc {
	return func(c *Context) error {
		res, err := fn(c)
		if err != nil {
			return err
		}
		if !c.Written() {
			return c.JSON(http.StatusOK, res)
		}
		return nil
	}
}

// Adapt wraps a stdlib http.HandlerFunc as a framework HandlerFunc.
func Adapt(fn func(http.ResponseWriter, *http.Request)) HandlerFunc {
	return func(c *Context) error {
		fn(c.Response(), c.Request())
		return nil
	}
}
