package sandbox

import "net/http"

func userClient() *http.Client {
	return &http.Client{Transport: userTransport{base: http.DefaultTransport}}
}

type userTransport struct {
	base http.RoundTripper
}

func (t userTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if user := req.Header.Get("User"); user != "" {
		req.SetBasicAuth(user, "")
		req.Header.Del("User")
	}
	return t.base.RoundTrip(req)
}
