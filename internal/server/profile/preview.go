package profile

import (
	"context"
	"os"
)

// PreviewUpdate reads/fetches an edit without committing its URL or bytes.
// Validation of a draft must never replace the last successful subscription.
func (s *Store) PreviewUpdate(id string, in UpdateInput) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.findMetaLocked(id)
	if err != nil {
		return "", err
	}
	if in.URL != nil || in.UserAgent != nil {
		if meta.Type != TypeRemote {
			return "", ErrNotRemote
		}
		if in.URL != nil {
			meta.URL = *in.URL
		}
		if in.UserAgent != nil {
			meta.UserAgent = *in.UserAgent
		}
		body, _, err := s.fetcher.Fetch(context.Background(), meta.URL, meta.UserAgent)
		return body, err
	}
	if in.Content != nil {
		return *in.Content, nil
	}
	body, err := os.ReadFile(s.profilePath(id))
	return string(body), err
}
