package main

import (
	"net"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/vskurikhin/fasthttpproxy/fasthttpproxy"
)

func main() {
	s := &fasthttp.Server{
		StreamRequestBody:            true,
		MaxRequestBodySize:           0,
		DisablePreParseMultipartForm: true,
		ReduceMemoryUsage:            true,
		Handler:                      proxyHandler,
	}

	ln, _ := net.Listen("tcp", ":8080")
	s.Serve(ln) //nolint:errcheck
}

func proxyHandler(ctx *fasthttp.RequestCtx) {
	targetHost := string(ctx.URI().Host())
	targetPort := map[string]string{
		"http": "80", "https": "443",
	}[string(ctx.URI().Scheme())]
	targetAddr := targetHost
	if !strings.Contains(targetHost, ":") {
		targetAddr = net.JoinHostPort(targetHost, targetPort)
	}

	c := &fasthttp.HostClient{
		Addr:                targetAddr,
		Dial:                fasthttpproxy.FasthttpHTTPDialerDualStackTimeout("", 30*time.Second),
		IsTLS:               false,
		MaxConns:            100,
		MaxIdleConnDuration: time.Minute,
	}

	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	req.Header.SetMethodBytes(ctx.Method())
	req.SetRequestURI(string(ctx.URI().RequestURI()))
	req.SetHost(targetHost)
	req.SetBodyStream(ctx.RequestBodyStream(), -1)

	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseResponse(resp)

	err := c.DoDeadline(req, resp, time.Now().Add(10*time.Minute))
	if err != nil {
		ctx.Error("proxy error: "+err.Error(), fasthttp.StatusBadGateway)
		return
	}

	resp.Header.CopyTo(&ctx.Response.Header)
	ctx.SetStatusCode(resp.StatusCode())
	ctx.Response.ImmediateHeaderFlush = true
	ctx.SetBodyStream(resp.BodyStream(), -1)
}
