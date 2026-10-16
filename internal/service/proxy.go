package service

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"fn2auth/internal/config"
	"fn2auth/internal/instance"
)

func proxy(cfg config.Config, worker *instance.Worker) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{Transport: worker.Transport, Rewrite: func(proxy *httputil.ProxyRequest) {
		proxy.Out.URL.Scheme = "http"
		proxy.Out.URL.Host = "localhost"
		proxy.Out.Host = proxy.In.Host
		for name := range proxy.Out.Header {
			lower := strings.ReplaceAll(strings.ToLower(name), "_", "-")
			if strings.HasPrefix(lower, "x-trim-") || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-fn2auth-") || lower == "forwarded" || lower == "authorization" || lower == "proxy-authorization" || lower == "remote-user" || lower == "cookie" {
				proxy.Out.Header.Del(name)
			}
		}
		proxy.Out.Header.Set("X-Fn2auth-Instance", worker.Secret)
		session := "fn2auth_" + strconv.FormatUint(uint64(worker.State.User.UID), 10) + "_session"
		for _, cookie := range proxy.In.Cookies() {
			if cookie.Name == session || cookie.Name == "XSRF-TOKEN" {
				proxy.Out.AddCookie(cookie)
			}
		}
	}, ModifyResponse: func(response *http.Response) error {
		response.Header.Set("Cache-Control", "no-store")
		response.Header.Set("X-Frame-Options", "SAMEORIGIN")
		response.Header.Set("X-Content-Type-Options", "nosniff")
		response.Header.Del("Access-Control-Allow-Origin")
		response.Header.Del("Access-Control-Allow-Credentials")
		if raw := response.Header.Get("Location"); raw != "" {
			target, err := url.Parse(raw)
			if err != nil || target.User != nil || target.Host != "" && target.Host != response.Request.Host {
				return errors.New("unsafe backend redirect")
			}
			target.Scheme = ""
			target.Host = ""
			if strings.HasPrefix(target.Path, "/") && !strings.HasPrefix(target.Path, cfg.Prefix+"/") && target.Path != cfg.Prefix {
				target.Path = cfg.Prefix + target.Path
			}
			response.Header.Set("Location", target.String())
		}
		cookies := response.Cookies()
		response.Header.Del("Set-Cookie")
		for _, cookie := range cookies {
			cookie.Domain = ""
			cookie.Path = cfg.Prefix + "/"
			cookie.SameSite = http.SameSiteStrictMode
			response.Header.Add("Set-Cookie", cookie.String())
		}
		return nil
	}, ErrorHandler: func(writer http.ResponseWriter, _ *http.Request, _ error) {
		failure(writer, "runtime_unavailable", 502)
	}}
}
