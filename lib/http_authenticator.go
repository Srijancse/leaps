/*
Copyright (c) 2014 Ashley Jeffs

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, sub to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/

package lib

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/jeffail/util/log"
)

/*--------------------------------------------------------------------------------------------------
 */

/*
HTTPAuthenticatorConfig - A config object for the HTTP API authentication object.
*/
type HTTPAuthenticatorConfig struct {
	Path         string `json:"path" yaml:"path"`
	ExpiryPeriod int64  `json:"expiry_period_s" yaml:"expiry_period_s"`
}

/*
NewHTTPAuthenticatorConfig - Returns a default config object for a HTTPAuthenticator.
*/
func NewHTTPAuthenticatorConfig() HTTPAuthenticatorConfig {
	return HTTPAuthenticatorConfig{
		Path:         "",
		ExpiryPeriod: 60,
	}
}

/*--------------------------------------------------------------------------------------------------
 */

func (h *HTTPAuthenticator) serveGenerateToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST endpoint only", http.StatusMethodNotAllowed)
		return
	}

	bytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		h.logger.Errorf("Failed to read request body: %v\n", err)
		http.Error(w, "Bad request: could not read body", http.StatusBadRequest)
		return
	}

	var bodyObj struct {
		Key string `json:"key_value"`
	}
	if err = json.Unmarshal(bytes, &bodyObj); err != nil {
		h.logger.Errorf("Failed to parse request body: %v\n", err)
		http.Error(w, "Bad request: could not parse body", http.StatusBadRequest)
		return
	}

	if 0 == len(bodyObj.Key) {
		h.logger.Errorln("User ID not found in request body")
		http.Error(w, "Bad request: no user id found", http.StatusBadRequest)
		return
	}

	token := GenerateStampedUUID()

	h.mutex.Lock()

	h.tokens[token] = tokenMapValue{
		value:   bodyObj.Key,
		expires: time.Now().Add(time.Second * time.Duration(h.config.HTTPConfig.ExpiryPeriod)),
	}
	h.mutex.Unlock()

	resBytes, err := json.Marshal(struct {
		Token string `json:"token"`
	}{
		Token: token,
	})
	if err != nil {
		h.logger.Errorf("Failed to generate JSON response: %v\n", err)
		http.Error(w, "Failed to generate response", http.StatusInternalServerError)
		return
	}

	w.Write(resBytes)
	w.Header().Add("Content-Type", "application/json")

	h.clearExpiredTokens()
}

/*--------------------------------------------------------------------------------------------------
 */

type tokenMapValue struct {
	value   string
	expires time.Time
}

type tokensMap map[string]tokenMapValue

/*
HTTPAuthenticator - Uses the admin HTTP server to expose an endpoint for submitting authentication
tokens.
*/
type HTTPAuthenticator struct {
	logger *log.Logger
	stats  *log.Stats
	config TokenAuthenticatorConfig
	mutex  sync.RWMutex
	tokens tokensMap
}

/*
NewHTTPAuthenticator - Creates an HTTPAuthenticator using the provided configuration.
*/
func NewHTTPAuthenticator(config TokenAuthenticatorConfig, logger *log.Logger, stats *log.Stats) *HTTPAuthenticator {
	return &HTTPAuthenticator{
		logger: logger.NewModule(":http_auth"),
		stats:  stats,
		config: config,
		mutex:  sync.RWMutex{},
		tokens: tokensMap{},
	}
}

/*--------------------------------------------------------------------------------------------------
 */

/*
clearExpiredTokens - Purges our expired tokens from the map.
*/
func (h *HTTPAuthenticator) clearExpiredTokens() {
	expiredTokens := []string{}

	h.mutex.RLock()
	for token, val := range h.tokens {
		if val.expires.Before(time.Now()) {
			expiredTokens = append(expiredTokens, token)
		}
	}
	h.mutex.RUnlock()

	if len(expiredTokens) > 0 {
		h.mutex.Lock()
		for _, token := range expiredTokens {
			delete(h.tokens, token)
		}
		h.mutex.Unlock()
	}
}

/*--------------------------------------------------------------------------------------------------
 */

/*
AuthoriseCreate - Checks whether a specific token has been generated for a user through the HTTP
authentication endpoint for creating a new document.
*/
func (h *HTTPAuthenticator) AuthoriseCreate(token, userID string) bool {
	if !h.config.AllowCreate {
		return false
	}

	h.mutex.RLock()
	defer h.mutex.RUnlock()

	if tObj, ok := h.tokens[token]; ok {
		if tObj.value == userID {
			delete(h.tokens, token)
			return true
		}
	}
	return false
}

/*
AuthoriseJoin - Checks whether a specific token has been generated for a document through the HTTP
authentication endpoint for joining that aforementioned document.
*/
func (h *HTTPAuthenticator) AuthoriseJoin(token, documentID string) bool {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	if tObj, ok := h.tokens[token]; ok {
		if tObj.value == documentID {
			delete(h.tokens, token)
			return true
		}
	}
	return false
}

/*
RegisterHandlers - Register endpoints for adding new auth tokens.
*/
func (h *HTTPAuthenticator) RegisterHandlers(register PubPrivEndpointRegister) error {
	if err := register.RegisterPrivate(
		path.Join(h.config.HTTPConfig.Path, "create"),
		`Generate an authentication token for creating a new document, POST: {"key_value":"<user_id>"}`,
		h.serveGenerateToken,
	); err != nil {
		return err
	}
	return register.RegisterPrivate(
		path.Join(h.config.HTTPConfig.Path, "join"),
		`Generate an authentication token for joining an existing document, POST: {"key_value":"<document_id>"}`,
		h.serveGenerateToken,
	)
}

/*--------------------------------------------------------------------------------------------------
 */