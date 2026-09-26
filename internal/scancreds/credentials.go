package scancreds

import "errors"

type Credentials struct {
	Cookie        string `json:"cookie,omitempty"`
	Authorization string `json:"authorization,omitempty"`
}

func (c Credentials) Validate() error {
	for _, value := range []string{c.Cookie, c.Authorization} {
		if len(value) > 4096 {
			return errors.New("session header too long")
		}
		for i := 0; i < len(value); i++ {
			if value[i] < 0x20 || value[i] > 0x7e {
				return errors.New("invalid session header")
			}
		}
	}
	return nil
}

func (c Credentials) Headers(userAgent string) map[string][]string {
	headers := map[string][]string{"User-Agent": {userAgent}}
	if c.Cookie != "" {
		headers["Cookie"] = []string{c.Cookie}
	}
	if c.Authorization != "" {
		headers["Authorization"] = []string{c.Authorization}
	}
	return headers
}
