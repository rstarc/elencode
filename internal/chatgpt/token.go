package chatgpt

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
)

// ErrSignedOut means the issuer turned the refresh token down. It will do so
// again, so nothing short of signing in again gets the session back.
var ErrSignedOut = errors.New("the ChatGPT login has expired or was revoked: sign in again with /login chatgpt, or `elencode login chatgpt`")

// Tokens is a ChatGPT login: what the backend wants on every request, and what
// renews it.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	// AccountID is read out of IDToken once, at login, rather than on every
	// request: it is a header the backend requires.
	AccountID string `json:"account_id"`
}

// Email is the address of the account the login is for, or "" when the id
// token does not say. For telling the user which of their accounts it is.
func (t Tokens) Email() string {
	c, err := decodeClaims(t.IDToken)
	if err != nil {
		return ""
	}
	if c.Email != "" {
		return c.Email
	}
	return c.Profile.Email
}

// Plan is the ChatGPT plan the account is on, such as "plus", or "" when the
// id token does not say.
func (t Tokens) Plan() string {
	c, err := decodeClaims(t.IDToken)
	if err != nil {
		return ""
	}
	return c.Auth.ChatGPTPlanType
}

// exchange trades an authorization code, and the verifier its challenge was
// made from, for the login's first tokens.
func (o OAuth) exchange(ctx context.Context, code, verifier, redirectURI string) (Tokens, error) {
	token, err := o.config(redirectURI).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Tokens{}, fmt.Errorf("exchanging the authorization code: %w", err)
	}
	if token.RefreshToken == "" {
		return Tokens{}, errors.New("the issuer returned no refresh token, so the login could not be renewed")
	}
	idToken, _ := token.Extra("id_token").(string)
	account, err := accountID(idToken)
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, IDToken: idToken, AccountID: account}, nil
}

// refresh renews old. The account stays the one the login was made for: a
// refresh cannot move it, and re-reading it would only add a way to fail.
func (o OAuth) refresh(ctx context.Context, old Tokens) (Tokens, error) {
	// A token with no access token is never valid, so this goes straight to
	// the issuer. x/oauth2 keeps the old refresh token if no new one comes back.
	token, err := o.config("").TokenSource(ctx, &oauth2.Token{RefreshToken: old.RefreshToken}).Token()
	var rejected *oauth2.RetrieveError
	if errors.As(err, &rejected) && (rejected.Response.StatusCode == http.StatusBadRequest || rejected.Response.StatusCode == http.StatusUnauthorized) {
		return Tokens{}, fmt.Errorf("%w (%w)", ErrSignedOut, err)
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("refreshing the ChatGPT login: %w", err)
	}

	renewed := old
	renewed.AccessToken = token.AccessToken
	renewed.RefreshToken = token.RefreshToken
	if idToken, _ := token.Extra("id_token").(string); idToken != "" {
		renewed.IDToken = idToken
	}
	return renewed, nil
}
