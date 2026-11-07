package archive

import "net/url"

// CheckHTTPSourceURL applies the HTTP collector's first-URL policy without
// resolving or fetching anything. It includes only audited query exceptions.
func CheckHTTPSourceURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return ErrInvalid
	}
	_, err = admitFirstURL(u)
	return err
}
