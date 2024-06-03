package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	fhttp "github.com/Danny-Dasilva/fhttp"
	"github.com/Noooste/azuretls-client"
	"github.com/stanislav-milchev/tls-impersonator/browser"
)

var (
	urlHeaderName      = getEnv("TLS_URL", "x-tls-url")
	proxyHeaderName    = getEnv("TLS_PROXY", "x-tls-proxy")
	streamHeaderName   = getEnv("TLS_STREAM", "x-tls-stream")
	redirectHeaderName = getEnv("TLS_REDIRECT", "x-tls-allowredirect")
	timeoutHeaderName  = getEnv("TLS_TIMEOUT", "x-tls-timeout")
)

func main() {
	port := ":8082"
	log.Printf("Listening on localhost%s", port)
	fhttp.HandleFunc("/", HandleReq)
	fhttp.HandleFunc("/isalive", HandleIsAlive)
	// dev testing endpoints
	fhttp.HandleFunc("/sleep", TimeoutChecker)
	fhttp.HandleFunc("/headers", handleHeaderYoink)

	err := fhttp.ListenAndServe(port, nil)
	if err != nil {
		log.Fatalln("Error starting the HTTP server:", err)
	}
}

// handleHeaderYoink is a helper endpoint to get the header values of the current request
func handleHeaderYoink(_ fhttp.ResponseWriter, r *fhttp.Request) {
	for header, value := range r.Header {
		fmt.Printf("{\"%s\", \"%s\"}\n", header, value[0])
	}
}

// TimeoutChecker is a helper endpoint to debug timeouts
func TimeoutChecker(w fhttp.ResponseWriter, r *fhttp.Request) {
	time.Sleep(time.Second * 45)
}

func HandleIsAlive(w fhttp.ResponseWriter, r *fhttp.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(fhttp.StatusOK)
	fmt.Fprintf(w, `{"isalive":true}`)
}

// HandleReq takes the incoming request, parses it, sends it towards the target host
func HandleReq(w fhttp.ResponseWriter, r *fhttp.Request) {
	session, req, err := NewRequest(r)
	if err != nil {
		log.Print(err)
		w.WriteHeader(fhttp.StatusBadRequest)
		return
	}

	defer session.Close()
	SetHeaders(session, r.Header)
	res, err := session.Do(req)

	if err != nil {
		if strings.Contains(err.Error(), "timeout") {
			fmt.Print("timeout\n", err)
			w.WriteHeader(fhttp.StatusRequestTimeout)
			return
		} else {
			// TODO: EOF error encountered here at one point. Doesn't seem to happen now.
			// Potentially could be 'Connection' header issue
			fmt.Print("other error:\n", err)
			w.WriteHeader(fhttp.StatusInternalServerError)
			return
		}
	}

	// Forward the headers received
	w.WriteHeader(res.StatusCode)
	for h, v := range res.Header {
		// Response we get is already decoded so this header will only cause issues with the
		// client used for the request
		if "content-encoding" == strings.ToLower(h) {
			continue
		}
		if len(v) > 0 {
			w.Header().Set(h, v[0])
		} else {
			fmt.Printf("Skipping \"%s\" header with invalid value", h)
			continue
		}
	}

	stream := r.Header.Get(streamHeaderName) != ""
	// Either return buffered response or a stream
	if !stream {
		// Read the body and return buffered response
		if readBody, readErr := res.ReadBody(); readErr == nil {
			w.Write(readBody)
		} else {
			log.Printf("Error buffering response: %v", readErr)
		}
	} else {
		// Stream the response body
		_, err = io.Copy(w, res.RawBody)
		if err != nil {
			log.Printf("Error streaming response: %v", err)
		}

		res.RawBody.Close()
	}
}

// NewRequest opens a new azuretls session and a request, and sets it up with url,
// proxy, headers, cookies, redirects and timeouts
func NewRequest(r *fhttp.Request) (*azuretls.Session, *azuretls.Request, error) {
	// Open and set-up session
	session := azuretls.NewSession()
	session.EnableLog()

	// Parse and validate request URL
	urlHeader := r.Header.Get(urlHeaderName)

	if urlHeader == "" {
		return nil, nil, fmt.Errorf(
			"no valid request URL supplied via '%s'; skipping request", urlHeaderName,
		)
	}

	// Parse redirects
	disableRedirects := r.Header.Get(redirectHeaderName) != ""

	// Parse timeout
	timeoutHeader := r.Header.Get(timeoutHeaderName)
	t, err := strconv.Atoi(timeoutHeader)
	if err != nil || t <= 0 {
		// Probably dont log that on every request? Do it once and disable a flag or sth
		// log.Println("Invalid timeout value supplied, defaulting to 30s.")
		t = 30
	}
	timeout := time.Duration(t) * time.Second
	session.SetTimeout(timeout)

	// Parse proxy
	proxy := r.Header.Get(proxyHeaderName)
	session.SetProxy(proxy)

	req := &azuretls.Request{
		Method:           r.Method,
		Url:              urlHeader,
		DisableRedirects: disableRedirects,
		IgnoreBody:       true,
		Body:             r.Body,
	}
	return session, req, nil
}

// SetHeaders sets the custom headers received in the server to the session
func SetHeaders(s *azuretls.Session, headers fhttp.Header) {
	browserHeaders := browser.Chrome126
	customHeaderNames := []string{
		urlHeaderName,
		proxyHeaderName,
		redirectHeaderName,
		timeoutHeaderName,
		streamHeaderName,
	}
Outer:
	for k, v := range headers {
		// Dont send the custom request headers
		for _, header := range customHeaderNames {
			if strings.ToLower(header) == strings.ToLower(k) {
				continue Outer
			}
		}

		exist := browserHeaders.Get(strings.ToLower(k)) != ""
		if !exist {
			browserHeaders = append(browserHeaders, []string{k, v[0]})
			//fmt.Printf("added %s\nwith val: %s\n", k, v[0])
		}
	}

	s.OrderedHeaders = browserHeaders
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value

	}
	return fallback
}
