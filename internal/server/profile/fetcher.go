package profile

import "net/http"

// NewHTTPFetcher keeps the existing HTTP/status/usage-header behavior while
// allowing an embedding application to choose its transport (for example,
// direct subscription downloads independently of a broken proxy route).
func NewHTTPFetcher(client *http.Client) Fetcher {
	return &httpFetcher{timeout: defaultSubscriptionTimeout, client: client}
}
