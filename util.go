package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/viper"
	"github.com/theckman/httpforwarded"

	log "github.com/sirupsen/logrus"
)

// formatBaseURL takes a hostname (baseHost) and a base path
// and joins them.  Both are parsed as URLs (using net/url) and
// then joined to ensure a properly formed URL.
// net/url does not support parsing hostnames without a scheme
// (e.g. example.com is invalid; http://example.com is valid).
// serverURLHost ensures a scheme is added.
func formatBaseURL(baseHost string, basePath string) string {
	urlHost, err := url.Parse(baseHost)
	if err != nil {
		log.Fatal(err)
	}
	urlPath, err := url.Parse(basePath)
	if err != nil {
		log.Fatal(err)
	}
	return strings.TrimRight(urlHost.ResolveReference(urlPath).String(), "/")
}

// serverURLBase returns the base server URL
// that the client used to access this service.
// All pg_tileserv routes are mounted relative to
// this URL (including path, if specified by the
// BasePath config option)
func serverURLBase(r *http.Request) string {
	baseHost := serverURLHost(r)
	basePath := viper.GetString("BasePath")

	return formatBaseURL(baseHost, basePath)
}

func serverWsBase(r *http.Request) string {
	urlBase := serverURLBase(r)
	u, err := url.Parse(urlBase)
	if err != nil {
		log.Fatal(err)
	}
	scheme := "ws"
	if r.TLS != nil {
		scheme = "wss"
	}
	u.Scheme = scheme
	return u.String()
}

// serverURLHost returns the host (and scheme)
// for this service.
// In the case of access via a proxy service, if
// the standard headers are set, we return that
// hostname. If necessary the automatic calculation
// can be over-ridden by setting the "UrlBase"
// configuration option.
func serverURLHost(r *http.Request) string {
	// Use configuration file settings if we have them
	configURL := viper.GetString("UrlBase")
	if configURL != "" {
		return configURL
	}

	// Preferred scheme
	ps := "http"
	if r.TLS != nil {
		ps = "https"
	}

	// Preferred host:port
	ph := strings.TrimRight(r.Host, "/")

	// Check for the IETF standard "Forwarded" header
	// for reverse proxy information
	xf := http.CanonicalHeaderKey("Forwarded")
	if f, ok := r.Header[xf]; ok {
		if fm, err := httpforwarded.Parse(f); err == nil {
			if len(fm["host"]) > 0 && len(fm["proto"]) > 0 {
				ph = fm["host"][0]
				ps = fm["proto"][0]
				return fmt.Sprintf("%v://%v", ps, ph)
			}
		}
	}

	// Check the X-Forwarded-Host and X-Forwarded-Proto
	// headers
	xfh := http.CanonicalHeaderKey("X-Forwarded-Host")
	if fh, ok := r.Header[xfh]; ok {
		ph = fh[0]
	}
	xfp := http.CanonicalHeaderKey("X-Forwarded-Proto")
	if fp, ok := r.Header[xfp]; ok {
		ps = fp[0]
	}

	return fmt.Sprintf("%v://%v", ps, ph)
}
