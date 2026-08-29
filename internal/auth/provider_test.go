package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExchangeGoogleOIDC(t *testing.T) {
	var stsCalled bool
	var iamCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/sts":
			stsCalled = true
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload["subjectToken"] != "harness-token" {
				t.Errorf("subject token = %q", payload["subjectToken"])
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"access_token":"federated-token"}`))
		case strings.HasSuffix(request.URL.Path, ":generateAccessToken"):
			iamCalled = true
			if got := request.Header.Get("Authorization"); got != "Bearer federated-token" {
				t.Errorf("Authorization = %q", got)
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"accessToken":"service-account-token"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	token, err := exchangeGoogleOIDC(
		context.Background(),
		server.Client(),
		server.URL+"/sts",
		server.URL+"/iam",
		"harness-token",
		"123",
		"pool",
		"provider",
		"builder@example.iam.gserviceaccount.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	if token != "service-account-token" {
		t.Fatalf("token = %q", token)
	}
	if !stsCalled || !iamCalled {
		t.Fatalf("STS called = %t, IAM called = %t", stsCalled, iamCalled)
	}
}

func TestGetAADAccessTokenViaClientAssertion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/tenant/oauth2/v2.0/token" {
			http.NotFound(response, request)
			return
		}
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		if got := request.Form.Get("client_assertion"); got != "harness-token" {
			t.Errorf("client_assertion = %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"access_token":"aad-token"}`))
	}))
	defer server.Close()

	token, err := getAADAccessTokenViaClientAssertion(
		context.Background(),
		server.Client(),
		server.URL,
		"tenant",
		"client",
		"harness-token",
	)
	if err != nil {
		t.Fatal(err)
	}
	if token != "aad-token" {
		t.Fatalf("token = %q", token)
	}
}

func TestValidateAzureAuthority(t *testing.T) {
	t.Parallel()
	for _, authority := range []string{
		"",
		"https://login.microsoftonline.com",
		"https://login.microsoftonline.com/",
	} {
		if err := validateAzureAuthority(authority); err != nil {
			t.Fatalf("validateAzureAuthority(%q): %v", authority, err)
		}
	}
	for _, authority := range []string{
		"http://login.microsoftonline.com",
		"https://attacker.example",
		"https://login.microsoftonline.com/tenant",
		"https://login.microsoftonline.com:443",
		"https://login.microsoftonline.us",
		"https://login.chinacloudapi.cn",
	} {
		if err := validateAzureAuthority(authority); err == nil {
			t.Fatalf("validateAzureAuthority(%q) unexpectedly succeeded", authority)
		}
	}
}

func TestHTTPClientDoesNotFollowRedirects(t *testing.T) {
	var redirected bool
	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		redirected = true
		response.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Location", target.URL)
		response.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client := httpClient(source.Client())
	response, err := client.Post(source.URL, "application/json", strings.NewReader(`{"token":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusTemporaryRedirect)
	}
	if redirected {
		t.Fatal("HTTP client followed a credential-bearing redirect")
	}
}

func TestExchangeACRToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		if got := request.Form.Get("access_token"); got != "aad-token" {
			t.Errorf("access_token = %q", got)
		}
		if got := request.Form.Get("service"); got != "registry.azurecr.io" {
			t.Errorf("service = %q", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"refresh_token":"acr-token"}`))
	}))
	defer server.Close()

	token, err := exchangeACRToken(
		context.Background(),
		server.Client(),
		server.URL,
		"tenant",
		"registry.azurecr.io",
		"aad-token",
	)
	if err != nil {
		t.Fatal(err)
	}
	if token != "acr-token" {
		t.Fatalf("token = %q", token)
	}
}
