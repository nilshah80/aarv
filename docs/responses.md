# Responses, files, and streaming guide

Aarv response helpers live on `*aarv.Context`. They provide JSON, text, HTML,
binary blobs, redirects, no-content responses, files, attachments, streaming,
SSE, uploads, and static files.

## Response helpers

```go
c.JSON(http.StatusOK, value)
c.JSONPretty(http.StatusOK, value)
c.Text(http.StatusOK, "ok")
c.HTML(http.StatusOK, "<h1>ok</h1>")
c.Blob(http.StatusOK, "application/octet-stream", data)
c.NoContent(http.StatusNoContent)
c.Redirect(http.StatusFound, "/login")
```

`c.JSON` uses the app codec. Swap codecs with `aarv.WithCodec`; see
[`docs/codecs.md`](codecs.md).

Handlers should write one response. If a helper has already written, returning
nil is normally the right path.

## Files and attachments

Serve a file inline:

```go
return c.File("./public/report.pdf")
```

Serve as a download:

```go
return c.Attachment("./exports/report.csv", "report.csv")
```

Keep paths under application-controlled directories. Do not pass unchecked user
input directly to file paths.

## Multipart uploads

Use typed binding for upload forms:

```go
type UploadReq struct {
    Title string             `form:"title" validate:"required"`
    File  *aarv.UploadedFile `file:"file" validate:"required"`
}

app.Post("/upload", aarv.BindReq(func(c *aarv.Context, req UploadReq) error {
    filename := filepath.Base(req.File.Filename)
    if err := c.SaveFile(req.File, filepath.Join(uploadDir, filename)); err != nil {
        return aarv.ErrInternal(err)
    }
    return c.NoContent(http.StatusCreated)
}), aarv.WithRouteMaxBodySize(20<<20))
```

Use `FileWith` or `FilesWith` for lower-level validation of file size, count,
and content type.

## Streaming

`c.Stream` writes an `io.Reader` directly to the response.

```go
return c.Stream(http.StatusOK, "text/plain", reader)
```

Streaming bypasses normal response buffering. Do not rely on `OnSend`,
`etag`, `encrypt`, or body-capturing `verboselog` for streaming responses.

## Server-sent events

Use `c.SSE()` for SSE responses.

```go
sse, err := c.SSE()
if err != nil {
    return err
}

return sse.Send("message", "connected")
```

SSE routes should avoid response-buffering middleware and fixed write
timeouts that terminate long-lived streams unexpectedly.

## Static files

`plugins/static` serves files from a root directory, supports cache headers,
SPA fallback, and disabled directory browsing by default.

```go
app.Use(static.New(static.Config{
    Root:   "./public",
    Prefix: "/assets",
    MaxAge: 3600,
}))
```

`Root` is required. `Browse` defaults to false. `SPA` serves the root index
file when a file is not found, which is useful for client-side routing.

## Compression

`plugins/compress` buffers enough response data to decide whether gzip or
deflate should be applied.

```go
app.Use(compress.New(compress.Config{
    MinSize:    1024,
    PreferGzip: true,
}))
```

Excluded content types default to images, video, audio, PDFs, and compressed
archives. Avoid compression on already-compressed data and long-lived streams.

## ETags

`plugins/etag` computes an ETag from response body bytes and handles
`If-None-Match`.

```go
app.Use(etag.New(etag.Config{Weak: true}))
```

ETag middleware buffers the whole response body. Use it only on bounded GET or
HEAD responses, not on downloads, SSE, streaming, or large payloads.

## Buffering behavior

Response-related middleware falls into three categories:

| Category | Examples | Notes |
|---|---|---|
| Stream-safe | routing, native middleware, `requestid`, `secure`, `cors`, `bodylimit` | Does not need full response body. |
| Bounded-buffer | `compress`, body-limited `verboselog` | Buffers within configured limits or thresholds. |
| Full-buffer | `etag`, `encrypt` | Buffers whole responses; avoid on large or streaming routes. |

When a route streams, keep its middleware stack minimal and stream-safe.

## Production checklist

- Use `WithRouteMaxBodySize` on upload routes.
- Validate upload content type and size before saving.
- Keep static file roots fixed and application-controlled.
- Avoid response-buffering middleware on streaming, SSE, and large downloads.
- Use compression only for compressible, bounded responses.
- Use ETags on cacheable bounded GET/HEAD responses.
- Let one handler write one response.
