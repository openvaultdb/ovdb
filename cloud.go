package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	openvaultdbcloud "github.com/openvaultdb/openvaultdb-go/cloud"
	"github.com/spf13/cobra"
	"github.com/strongo/deviceauth"
	"golang.org/x/oauth2"
)

const (
	defaultCloudURL = "https://cloud.openvaultdb.com"
	cloudClientID   = "ovdb-cli"
	cloudScope      = "account:read databases:read"
)

type cloudFlags struct {
	host            string
	insecureStorage bool
}

type cloudStoreSelection struct {
	store       deviceauth.Store
	description string
	insecure    bool
}

type cloudDependencies struct {
	httpClient    *http.Client
	login         func(context.Context, deviceauth.LoginOptions) (deviceauth.LoginResult, error)
	openBrowser   func(string) error
	deviceInfo    func() deviceauth.DeviceInfo
	userConfigDir func() (string, error)
}

type cloudIdentity struct {
	Subject   string `json:"sub"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	ClientID  string `json:"client_id"`
	Scope     string `json:"scope"`
	ExpiresAt string `json:"expires_at"`
}

func newCloudCmd() *cobra.Command {
	return newCloudCmdWithDependencies(cloudDependencies{
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		login:         deviceauth.Login,
		openBrowser:   deviceauth.OpenBrowser,
		deviceInfo:    cloudDeviceInfo,
		userConfigDir: os.UserConfigDir,
	})
}

func newCloudCmdWithDependencies(deps cloudDependencies) *cobra.Command {
	flags := &cloudFlags{host: strings.TrimSpace(os.Getenv("OVDB_CLOUD_HOST"))}
	if flags.host == "" {
		flags.host = defaultCloudURL
	}

	cmd := &cobra.Command{
		Use:   "cloud",
		Short: "Authenticate and manage OpenVaultDB Cloud access",
	}
	cmd.PersistentFlags().StringVar(
		&flags.host,
		"host",
		flags.host,
		"OpenVaultDB Cloud base URL",
	)
	cmd.PersistentFlags().BoolVar(
		&flags.insecureStorage,
		"insecure-storage",
		false,
		"store the cloud token in a plaintext 0600 file instead of the operating system keyring",
	)
	cmd.AddCommand(
		newCloudLoginCmd(flags, deps),
		newCloudStatusCmd(flags, deps),
		newCloudLogoutCmd(flags, deps),
		newCloudDatabasesCmd(flags, deps),
	)
	return cmd
}

func newCloudLoginCmd(flags *cloudFlags, deps cloudDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Sign in to OpenVaultDB Cloud through your browser",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			baseURL, err := normalizeCloudURL(flags.host)
			if err != nil {
				return err
			}
			selection, err := selectCloudStore(baseURL, flags.insecureStorage, deps.userConfigDir)
			if err != nil {
				return err
			}
			previous, err := selection.store.Load()
			if err != nil && !errors.Is(err, deviceauth.ErrCredentialNotFound) {
				return cloudStorageError(err)
			}
			if selection.insecure {
				_, _ = fmt.Fprintf(
					cmd.ErrOrStderr(),
					"Warning: --insecure-storage writes the access token unencrypted to %s.\n",
					selection.description,
				)
			}

			loginContext := context.WithValue(cmd.Context(), oauth2.HTTPClient, deps.httpClient)
			deviceInfo := deviceauth.DeviceInfo{}
			if deps.deviceInfo != nil {
				deviceInfo = deps.deviceInfo()
			}
			result, err := deps.login(loginContext, deviceauth.LoginOptions{
				OAuthConfig: cloudOAuthConfig(baseURL),
				DeviceInfo:  deviceInfo,
				OpenBrowser: deps.openBrowser,
				Output:      cmd.OutOrStdout(),
				ErrorOutput: cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}

			identity, err := fetchCloudIdentity(cmd.Context(), deps.httpClient, baseURL, result.Token.AccessToken)
			if err != nil {
				return cleanupIssuedTokenError(
					fmt.Errorf("validate issued cloud token: %w", err),
					revokeCloudToken(cmd.Context(), deps.httpClient, baseURL, result.Token.AccessToken),
				)
			}
			expiry := result.Token.Expiry
			if parsed, parseErr := time.Parse(time.RFC3339, identity.ExpiresAt); parseErr == nil {
				expiry = parsed
			}
			credential := deviceauth.Credential{
				AccessToken: result.Token.AccessToken,
				TokenType:   result.Token.TokenType,
				Expiry:      expiry,
				AccountID:   identity.Subject,
				AccountName: cloudAccountName(identity),
				Scopes:      strings.Fields(identity.Scope),
			}
			if credential.TokenType == "" {
				credential.TokenType = "Bearer"
			}
			if err := selection.store.Save(credential); err != nil {
				return cleanupIssuedTokenError(
					cloudStorageError(err),
					revokeCloudToken(cmd.Context(), deps.httpClient, baseURL, result.Token.AccessToken),
				)
			}

			if previous.AccessToken != "" && previous.AccessToken != result.Token.AccessToken {
				if err := revokeCloudToken(cmd.Context(), deps.httpClient, baseURL, previous.AccessToken); err != nil {
					_, _ = fmt.Fprintf(
						cmd.ErrOrStderr(),
						"Warning: signed in, but the previous cloud token could not be revoked: %v\n",
						err,
					)
				}
			}

			_, _ = fmt.Fprintf(
				cmd.OutOrStdout(),
				"Logged in to %s as %s.\nToken storage: %s\n",
				baseURL.Host,
				cloudAccountName(identity),
				selection.description,
			)
			return nil
		},
	}
}

func cloudDeviceInfo() deviceauth.DeviceInfo {
	hostname, _ := os.Hostname()
	return deviceauth.DeviceInfo{
		Name:          strings.TrimSpace(hostname),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		ClientVersion: strings.TrimSpace(appVersion),
	}
}

func newCloudStatusCmd(flags *cloudFlags, deps cloudDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the current OpenVaultDB Cloud login",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			baseURL, err := normalizeCloudURL(flags.host)
			if err != nil {
				return err
			}
			selection, err := selectCloudStore(baseURL, flags.insecureStorage, deps.userConfigDir)
			if err != nil {
				return err
			}
			credential, err := selection.store.Load()
			if errors.Is(err, deviceauth.ErrCredentialNotFound) {
				return fmt.Errorf("not logged in to %s; run 'ovdb cloud login'", baseURL.Host)
			}
			if err != nil {
				return cloudStorageError(err)
			}
			identity, err := fetchCloudIdentity(cmd.Context(), deps.httpClient, baseURL, credential.AccessToken)
			if err != nil {
				return fmt.Errorf("cloud credential is expired, revoked, or invalid: %w", err)
			}
			_, _ = fmt.Fprintf(
				cmd.OutOrStdout(),
				"%s\n  Logged in as: %s\n  Scopes: %s\n  Expires: %s\n  Token storage: %s\n",
				baseURL.Host,
				cloudAccountName(identity),
				identity.Scope,
				identity.ExpiresAt,
				selection.description,
			)
			return nil
		},
	}
}

func newCloudLogoutCmd(flags *cloudFlags, deps cloudDependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Revoke and remove the current OpenVaultDB Cloud login",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			baseURL, err := normalizeCloudURL(flags.host)
			if err != nil {
				return err
			}
			selection, err := selectCloudStore(baseURL, flags.insecureStorage, deps.userConfigDir)
			if err != nil {
				return err
			}
			credential, err := selection.store.Load()
			if errors.Is(err, deviceauth.ErrCredentialNotFound) {
				return fmt.Errorf("not logged in to %s", baseURL.Host)
			}
			if err != nil {
				return cloudStorageError(err)
			}
			if err := revokeCloudToken(cmd.Context(), deps.httpClient, baseURL, credential.AccessToken); err != nil {
				return fmt.Errorf("revoke cloud token (local credential retained): %w", err)
			}
			if err := selection.store.Delete(); err != nil {
				return fmt.Errorf("cloud token was revoked, but remove local credential: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Logged out of %s.\n", baseURL.Host)
			return nil
		},
	}
}

func cloudOAuthConfig(baseURL *url.URL) oauth2.Config {
	return oauth2.Config{
		ClientID: cloudClientID,
		Scopes:   []string{cloudScope},
		Endpoint: oauth2.Endpoint{
			DeviceAuthURL: baseURL.String() + "/oauth/device/code",
			TokenURL:      baseURL.String() + "/oauth/token",
			AuthStyle:     oauth2.AuthStyleInParams,
		},
	}
}

func selectCloudStore(
	baseURL *url.URL,
	insecure bool,
	userConfigDir func() (string, error),
) (cloudStoreSelection, error) {
	if !insecure {
		store, err := deviceauth.NewKeyringStore("ovdb", "cloud@"+baseURL.Host)
		if err != nil {
			return cloudStoreSelection{}, err
		}
		return cloudStoreSelection{
			store:       store,
			description: "operating system keyring",
		}, nil
	}

	configDir, err := userConfigDir()
	if err != nil {
		return cloudStoreSelection{}, fmt.Errorf("locate user configuration directory: %w", err)
	}
	credentialPath := filepath.Join(
		configDir,
		"ovdb",
		"cloud",
		base64.RawURLEncoding.EncodeToString([]byte(strings.ToLower(baseURL.Host)))+".json",
	)
	store, err := deviceauth.NewFileStore(credentialPath)
	if err != nil {
		return cloudStoreSelection{}, err
	}
	return cloudStoreSelection{
		store:       store,
		description: credentialPath,
		insecure:    true,
	}, nil
}

func normalizeCloudURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("--host must be an absolute OpenVaultDB Cloud URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("--host must contain only a scheme and host")
	}
	hostname := strings.ToLower(parsed.Hostname())
	loopback := hostname == "localhost"
	if address := net.ParseIP(hostname); address != nil {
		loopback = address.IsLoopback()
	}
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || !loopback) {
		return nil, errors.New("--host must use HTTPS (HTTP is allowed only for loopback development)")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = ""
	return parsed, nil
}

func fetchCloudIdentity(
	ctx context.Context,
	client *http.Client,
	baseURL *url.URL,
	accessToken string,
) (cloudIdentity, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL.String()+"/oauth/userinfo", nil)
	if err != nil {
		return cloudIdentity{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := client.Do(request)
	if err != nil {
		return cloudIdentity{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return cloudIdentity{}, cloudResponseError(response)
	}
	var identity cloudIdentity
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&identity); err != nil {
		return cloudIdentity{}, fmt.Errorf("decode cloud account: %w", err)
	}
	if identity.Subject == "" || identity.ClientID != cloudClientID {
		return cloudIdentity{}, errors.New("cloud account response is missing the expected identity or client")
	}
	return identity, nil
}

func revokeCloudToken(
	ctx context.Context,
	client *http.Client,
	baseURL *url.URL,
	accessToken string,
) error {
	values := url.Values{"token": {accessToken}, "client_id": {cloudClientID}}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		baseURL.String()+"/oauth/revoke",
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return cloudResponseError(response)
	}
	return nil
}

func cloudResponseError(response *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("%s (read error response: %v)", response.Status, err)
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		return errors.New(response.Status)
	}
	var oauthError struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal(body, &oauthError) == nil && oauthError.Error != "" {
		if oauthError.Description != "" {
			message = oauthError.Error + ": " + oauthError.Description
		} else {
			message = oauthError.Error
		}
	}
	return fmt.Errorf("%s: %s", response.Status, message)
}

func cloudAccountName(identity cloudIdentity) string {
	if identity.Name != "" && identity.Email != "" && identity.Name != identity.Email {
		return fmt.Sprintf("%s <%s>", identity.Name, identity.Email)
	}
	if identity.Name != "" {
		return identity.Name
	}
	if identity.Email != "" {
		return identity.Email
	}
	return identity.Subject
}

func cloudStorageError(err error) error {
	return fmt.Errorf(
		"access cloud credential storage: %w; use --insecure-storage only if plaintext token storage is acceptable",
		err,
	)
}

func newCloudDatabasesCmd(flags *cloudFlags, deps cloudDependencies) *cobra.Command {
	var space string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "databases",
		Aliases: []string{"database"},
		Short:   "List database registrations accessible in OpenVaultDB Cloud",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCloudDatabasesList(cmd, flags, deps, space, jsonOut)
		},
	}
	cmd.Flags().StringVar(&space, "space", "", "limit results to personal or an opaque Space ID")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON data only")
	cmd.AddCommand(newCloudDatabasesListCmd(flags, deps), newCloudDatabasesGetCmd(flags, deps))
	return cmd
}

func newCloudDatabasesListCmd(flags *cloudFlags, deps cloudDependencies) *cobra.Command {
	var space string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List database registrations accessible in OpenVaultDB Cloud",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCloudDatabasesList(cmd, flags, deps, space, jsonOut)
		},
	}
	cmd.Flags().StringVar(&space, "space", "", "limit results to personal or an opaque Space ID")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON data only")
	return cmd
}

func newCloudDatabasesGetCmd(flags *cloudFlags, deps cloudDependencies) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one OpenVaultDB Cloud database registration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, credential, err := cloudCatalogueClient(flags, deps)
			if err != nil {
				return err
			}
			response, err := client.GetDatabase(cmd.Context(), credential.AccessToken, args[0])
			if err != nil {
				return cloudCatalogueError(err)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(response.Database)
			}
			writeCloudDatabaseTable(cmd.OutOrStdout(), []openvaultdbcloud.Database{response.Database})
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON data only")
	return cmd
}

func runCloudDatabasesList(
	cmd *cobra.Command,
	flags *cloudFlags,
	deps cloudDependencies,
	space string,
	jsonOut bool,
) error {
	if space != "" && space != "personal" && !isCloudIdentifier(space) {
		return errors.New("--space must be personal or a URL-safe Space ID")
	}
	client, credential, err := cloudCatalogueClient(flags, deps)
	if err != nil {
		return err
	}
	databases, err := listCloudDatabases(cmd.Context(), client, credential.AccessToken, space)
	if err != nil {
		return cloudCatalogueError(err)
	}
	if jsonOut {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(databases)
	}
	if len(databases) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No cloud databases found.")
		return nil
	}
	writeCloudDatabaseTable(cmd.OutOrStdout(), databases)
	return nil
}

func cloudCatalogueClient(flags *cloudFlags, deps cloudDependencies) (*openvaultdbcloud.Client, deviceauth.Credential, error) {
	baseURL, err := normalizeCloudURL(flags.host)
	if err != nil {
		return nil, deviceauth.Credential{}, err
	}
	selection, err := selectCloudStore(baseURL, flags.insecureStorage, deps.userConfigDir)
	if err != nil {
		return nil, deviceauth.Credential{}, err
	}
	credential, err := selection.store.Load()
	if errors.Is(err, deviceauth.ErrCredentialNotFound) {
		return nil, deviceauth.Credential{}, fmt.Errorf("not logged in to %s; run 'ovdb cloud login'", baseURL.Host)
	}
	if err != nil {
		return nil, deviceauth.Credential{}, cloudStorageError(err)
	}
	if !containsCloudScope(credential.Scopes, "databases:read") {
		return nil, deviceauth.Credential{}, errors.New("cloud credential lacks databases:read; run 'ovdb cloud login' to grant database metadata access")
	}
	client, err := openvaultdbcloud.NewClient(baseURL.String(), openvaultdbcloud.WithHTTPClient(deps.httpClient))
	if err != nil {
		return nil, deviceauth.Credential{}, fmt.Errorf("configure cloud catalogue client: %w", err)
	}
	return client, credential, nil
}

func listCloudDatabases(
	ctx context.Context,
	client *openvaultdbcloud.Client,
	accessToken string,
	space string,
) ([]openvaultdbcloud.Database, error) {
	seenTokens := map[string]bool{"": true}
	var databases []openvaultdbcloud.Database
	pageToken := ""
	for {
		response, err := client.ListDatabases(ctx, accessToken, openvaultdbcloud.ListDatabasesRequest{
			Space:     space,
			PageSize:  openvaultdbcloud.MaxPageSize,
			PageToken: pageToken,
		})
		if err != nil {
			return nil, err
		}
		databases = append(databases, response.Databases...)
		if response.NextPageToken == "" {
			return databases, nil
		}
		if seenTokens[response.NextPageToken] {
			return nil, errors.New("cloud catalogue pagination returned a repeated page token")
		}
		seenTokens[response.NextPageToken] = true
		pageToken = response.NextPageToken
	}
}

func writeCloudDatabaseTable(writer io.Writer, databases []openvaultdbcloud.Database) {
	table := tabwriter.NewWriter(writer, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "ID\tNAME\tSPACE\tPROVIDER\tSTATUS")
	for _, database := range databases {
		space := database.SpaceName
		if space == "" {
			space = database.SpaceID
		}
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
			sanitizeCloudTerminalText(database.ID),
			sanitizeCloudTerminalText(database.Name),
			sanitizeCloudTerminalText(space),
			sanitizeCloudTerminalText(database.Provider),
			sanitizeCloudTerminalText(database.Status),
		)
	}
	_ = table.Flush()
}

func cloudCatalogueError(err error) error {
	var apiError *openvaultdbcloud.APIError
	if errors.As(err, &apiError) {
		switch apiError.Code {
		case "insufficient_scope":
			return errors.New("cloud access requires databases:read; run 'ovdb cloud login' to grant database metadata access")
		case "invalid_token":
			return errors.New("cloud credential is expired, revoked, or invalid; run 'ovdb cloud login'")
		}
		if apiError.StatusCode == http.StatusUnauthorized {
			return errors.New("cloud credential is expired, revoked, or invalid; run 'ovdb cloud login'")
		}
		return fmt.Errorf("cloud database catalogue request failed: %s", sanitizeCloudTerminalText(apiError.Error()))
	}
	return fmt.Errorf("cloud database catalogue request failed: %s", sanitizeCloudTerminalText(err.Error()))
}

func containsCloudScope(scopes []string, wanted string) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

func isCloudIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, byteValue := range []byte(value) {
		if (byteValue >= 'a' && byteValue <= 'z') || (byteValue >= 'A' && byteValue <= 'Z') ||
			(byteValue >= '0' && byteValue <= '9') || byteValue == '-' || byteValue == '_' {
			continue
		}
		return false
	}
	return true
}

func sanitizeCloudTerminalText(value string) string {
	const maxLength = 512
	var sanitized strings.Builder
	for _, character := range value {
		switch character {
		case '\n':
			sanitized.WriteString("\\n")
		case '\r':
			sanitized.WriteString("\\r")
		case '\t':
			sanitized.WriteString("\\t")
		case 0x1b:
			sanitized.WriteString("\\x1b")
		default:
			if character < 0x20 || character == 0x7f {
				fmt.Fprintf(&sanitized, "\\x%02x", character)
			} else {
				sanitized.WriteRune(character)
			}
		}
		if sanitized.Len() >= maxLength {
			return sanitized.String()[:maxLength]
		}
	}
	return sanitized.String()
}

func cleanupIssuedTokenError(primary, cleanup error) error {
	if cleanup == nil {
		return primary
	}
	return fmt.Errorf("%w; additionally, the newly issued token could not be revoked: %v", primary, cleanup)
}
