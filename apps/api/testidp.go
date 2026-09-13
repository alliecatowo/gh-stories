//go:build ghs_testidp

package main

import (
	"context"

	"github.com/alliecatowo/gh-stories/internal/api"
)

// CompleteTestAuthorization is present only in a build tagged `ghs_testidp`.
// In a normal build this method does not exist, so the API's type assertion
// for the demo sign-in route fails and the route is never registered.
func (a *authAdapter) CompleteTestAuthorization(ctx context.Context, login string) (*api.AuthResult, error) {
	res, err := a.svc.CompleteTestAuthorization(ctx, login)
	if err != nil {
		return nil, err
	}
	return &api.AuthResult{
		User: res.User, Flow: res.Flow, IsNewAccount: res.IsNewAccount,
	}, nil
}
