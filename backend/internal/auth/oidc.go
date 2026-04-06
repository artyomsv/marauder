// OIDC support via coreos/go-oidc.
//
// This file provides a small Provider wrapper. Wiring is optional — enabled
// via config.Config.OIDCEnabled. When OIDC is off, no provider is created
// and the /auth/oidc/* routes return 404.
package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/artyomsv/marauder/backend/internal/config"
)

// OIDCProvider bundles everything needed to run the authorization code flow.
type OIDCProvider struct {
	Provider    *oidc.Provider
	OAuth2      *oauth2.Config
	Verifier    *oidc.IDTokenVerifier
	RedirectURL string
}

// NewOIDCProvider discovers the issuer and returns a configured provider.
// Returns nil, nil if OIDC is disabled in config.
func NewOIDCProvider(ctx context.Context, cfg *config.Config) (*OIDCProvider, error) {
	if !cfg.OIDCEnabled {
		return nil, nil
	}
	if cfg.OIDCIssuer == "" || cfg.OIDCClientID == "" || cfg.OIDCRedirectURL == "" {
		return nil, errors.New("OIDC enabled but issuer/client_id/redirect_url not set")
	}

	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}

	oauthCfg := &oauth2.Config{
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  cfg.OIDCRedirectURL,
		Scopes:       cfg.OIDCScopes,
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID})

	return &OIDCProvider{
		Provider:    provider,
		OAuth2:      oauthCfg,
		Verifier:    verifier,
		RedirectURL: cfg.OIDCRedirectURL,
	}, nil
}

// ExchangeAndVerify trades an authorization code for tokens and validates
// the ID token. Returns the verified claims subject + issuer + basic profile.
type OIDCIdentity struct {
	Subject string
	Issuer  string
	Email   string
	Name    string
}

// Exchange exchanges a code and verifies the ID token.
func (p *OIDCProvider) Exchange(ctx context.Context, code string) (*OIDCIdentity, error) {
	tok, err := p.OAuth2.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("oauth2 exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return nil, errors.New("no id_token in response")
	}
	idt, err := p.Verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}
	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	_ = idt.Claims(&claims)
	return &OIDCIdentity{
		Subject: idt.Subject,
		Issuer:  idt.Issuer,
		Email:   claims.Email,
		Name:    claims.Name,
	}, nil
}
