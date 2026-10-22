package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

// Management is a session of Admin on one replica's management API.
type Management struct {
	*management.ClientWithResponses
}

// SignIn opens a session of Admin on the replica at baseURL. The session
// cookie works over plain HTTP on loopback, where the harness runs.
func SignIn(ctx context.Context, baseURL string) (*Management, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	c, err := management.NewClientWithResponses(baseURL+management.BaseURL, management.WithHTTPClient(&http.Client{Jar: jar}))
	if err != nil {
		return nil, err
	}
	res, err := c.SignInWithResponse(ctx, management.SignInRequest{Email: Admin.Email, Password: Admin.Password})
	if err != nil {
		return nil, fmt.Errorf("sign in as %s: %w", Admin.Email, err)
	}
	if res.JSON200 == nil {
		return nil, unexpected("sign in as "+Admin.Email, res.StatusCode(), res.Body)
	}
	return &Management{c}, nil
}

// StatusError is a management API answer other than the one an action
// expects.
type StatusError struct {
	Action string
	Status int
	Body   []byte
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: status %d: %s", e.Action, e.Status, strings.TrimSpace(string(e.Body)))
}

func unexpected(action string, status int, body []byte) error {
	return &StatusError{Action: action, Status: status, Body: body}
}
