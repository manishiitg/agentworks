package gmailinbound

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// PushVerifier caches Google's signing certificates for shared push ingress.
// Delivery identity is unrelated to the sender identity inside an email.
type PushVerifier struct {
	Audience  string
	Email     string
	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	expires   time.Time
	refreshed time.Time
	Fetch     func(context.Context) (map[string]*rsa.PublicKey, error)
}

func (v *PushVerifier) Verify(ctx context.Context, token string) error {
	if v.Audience == "" || v.Email == "" {
		return fmt.Errorf("missing Pub/Sub authentication settings")
	}
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		v.mu.Lock()
		defer v.mu.Unlock()
		if time.Now().After(v.expires) || v.keys[kid] == nil {
			// Unknown key IDs must not turn unauthenticated traffic into a
			// certificate-fetch loop. Google rotates keys infrequently.
			if time.Since(v.refreshed) < time.Minute {
				return nil, fmt.Errorf("unknown Google signing key")
			}
			v.refreshed = time.Now()
			fetch := v.Fetch
			if fetch == nil {
				fetch = googleSigningKeys
			}
			keys, e := fetch(ctx)
			if e != nil {
				return nil, e
			}
			v.keys = keys
			v.expires = time.Now().Add(time.Hour)
		}
		key := v.keys[kid]
		if key == nil {
			return nil, fmt.Errorf("unknown Google signing key")
		}
		return key, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(v.Audience), jwt.WithExpirationRequired())
	if err != nil {
		return err
	}
	issuer, _ := claims["iss"].(string)
	email, _ := claims["email"].(string)
	verified, _ := claims["email_verified"].(bool)
	if (issuer != "https://accounts.google.com" && issuer != "accounts.google.com") || email != v.Email || !verified {
		return fmt.Errorf("unexpected Pub/Sub service identity")
	}
	return nil
}
func googleSigningKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", "https://www.googleapis.com/oauth2/v1/certs", nil)
	if e != nil {
		return nil, e
	}
	resp, e := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Google signing certificate lookup failed")
	}
	var certs map[string]string
	if e = json.NewDecoder(resp.Body).Decode(&certs); e != nil {
		return nil, e
	}
	keys := map[string]*rsa.PublicKey{}
	for id, text := range certs {
		block, _ := pem.Decode([]byte(text))
		if block == nil {
			continue
		}
		cert, e := x509.ParseCertificate(block.Bytes)
		if e != nil {
			continue
		}
		if k, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			keys[id] = k
		}
	}
	return keys, nil
}
