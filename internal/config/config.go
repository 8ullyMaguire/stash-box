package config

import (
	"errors"
	"time"

	"github.com/spf13/viper"

	"github.com/stashapp/stash-box/internal/service/trust"
	"github.com/stashapp/stash-box/pkg/utils"
)

type S3Config struct {
	Endpoint      string            `mapstructure:"endpoint"`
	Bucket        string            `mapstructure:"bucket"`
	AccessKey     string            `mapstructure:"access_key"`
	Secret        string            `mapstructure:"secret"`
	MaxDimension  int               `mapstructure:"max_dimension"`
	UploadHeaders map[string]string `mapstructure:"upload_headers"`
}

type PostgresConfig struct {
	MaxOpenConns    int `mapstructure:"max_open_conns"`
	MaxIdleConns    int `mapstructure:"max_idle_conns"`
	ConnMaxLifetime int `mapstructure:"conn_max_lifetime"`
}

type OTelConfig struct {
	Endpoint   string  `mapstructure:"endpoint"`
	TraceRatio float64 `mapstructure:"trace_ratio"`
}

type ImageResizeConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	CachePath string `mapstructure:"cache_path"`
	MinSize   int    `mapstructure:"min_size"`
}

type AutocertConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Domain   string `mapstructure:"domain"`
	Email    string `mapstructure:"email"`
	CacheDir string `mapstructure:"cache_dir"`
}

type FrontendConfig struct {
	Path   string `mapstructure:"path"`   // directory holding the build (index.html + assets/)
	Prefix string `mapstructure:"prefix"` // URL mount point, e.g. "/v2"
}

type config struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	Database     string `mapstructure:"database"`
	ProfilerPort int    `mapstructure:"profiler_port"`

	HTTPUpgrade  bool `mapstructure:"http_upgrade"`
	IsProduction bool `mapstructure:"is_production"`

	// Key used to sign JWT tokens
	JWTSignKey string `mapstructure:"jwt_secret_key"`
	// Key used for session store
	SessionStoreKey string `mapstructure:"session_store_key"`

	// Invite settings
	RequireInvite     bool     `mapstructure:"require_invite"`
	RequireActivation bool     `mapstructure:"require_activation"`
	ActivationExpiry  int      `mapstructure:"activation_expiry"`
	EmailCooldown     int      `mapstructure:"email_cooldown"`
	DefaultUserRoles  []string `mapstructure:"default_user_roles"`

	// URL link for contributor guidelines for submitting edits
	GuidelinesURL string `mapstructure:"guidelines_url"`
	// Number of approved edits before user automatically gets VOTE role
	VotePromotionThreshold int `mapstructure:"vote_promotion_threshold"`
	// Number of positive votes required for immediate approval
	VoteApplicationThreshold int `mapstructure:"vote_application_threshold"`
	// Duration, in seconds, of the voting period
	VotingPeriod int `mapstructure:"voting_period"`
	// Duration, in seconds, of the minimum voting period for destructive edits
	MinDestructiveVotingPeriod int `mapstructure:"min_destructive_voting_period"`
	// Interval between checks for completed voting periods
	VoteCronInterval string `mapstructure:"vote_cron_interval"`
	// Number of times an edit can be updated by the creator
	EditUpdateLimit int `mapstructure:"edit_update_limit"`
	// Minimum trust level at which a user may update another user's edit.
	//
	// Upstream PR #708 allowed admins to do this; this knob generalises it to a
	// trust threshold, because "who may amend moderation history" is an operator
	// policy question and the answer differs per instance. See
	// GetEditUpdateMinTrustLevel for the semantics, which are not simply "level
	// >= this".
	EditUpdateMinTrustLevel int `mapstructure:"edit_update_min_trust_level"`
	// Require all scene create edits to be submitted via drafts
	RequireSceneDraft bool `mapstructure:"require_scene_draft"`
	// Require the TagRole or Admin to edit tags
	RequireTagRole bool `mapstructure:"require_tag_role"`

	// Email settings
	EmailHost    string `mapstructure:"email_host"`
	EmailPort    int    `mapstructure:"email_port"`
	EmailUser    string `mapstructure:"email_user"`
	EmailPW      string `mapstructure:"email_password"`
	EmailFrom    string `mapstructure:"email_from"`
	EmailTLSMode string `mapstructure:"email_tls_mode"`
	HostURL      string `mapstructure:"host_url"`

	// Image storage settings
	ImageLocation    string `mapstructure:"image_location"`
	ImageBackend     string `mapstructure:"image_backend"`
	FaviconPath      string `mapstructure:"favicon_path"`
	ImageMaxSize     int    `mapstructure:"image_max_size"`
	ImageJpegQuality int    `mapstructure:"image_jpeg_quality"`

	// Logging options
	LogFile     string `mapstructure:"logFile"`
	UserLogFile string `mapstructure:"userLogFile"`
	LogOut      bool   `mapstructure:"logOut"`
	LogLevel    string `mapstructure:"logLevel"`

	S3 struct {
		S3Config `mapstructure:",squash"`
	}

	Postgres struct {
		PostgresConfig `mapstructure:",squash"`
	}

	OTel struct {
		OTelConfig `mapstructure:",squash"`
	}

	// revive:disable-next-line
	Image_Resizing struct {
		ImageResizeConfig `mapstructure:",squash"`
	}

	Autocert struct {
		AutocertConfig `mapstructure:",squash"`
	}

	PHashDistance int `mapstructure:"phash_distance"`

	Title string `mapstructure:"title"`

	// Additional on-disk frontend builds mounted at their own prefixes,
	// served alongside the embedded UI at /.
	Frontends []FrontendConfig `mapstructure:"frontends"`

	DraftTimeLimit int `mapstructure:"draft_time_limit"`

	// Number of days to retain mod audit logs (0 to disable logging)
	ModAuditRetentionDays int `mapstructure:"mod_audit_retention_days"`

	CSP string `mapstructure:"csp"`
}

var JWTSignKey = "jwt_secret_key"
var SessionStoreKey = "session_store_key"
var Database = "database"

type ImageBackendType string

const (
	FileBackend ImageBackendType = "file"
	S3Backend   ImageBackendType = "s3"
)

var defaultUserRoles = []string{"READ", "VOTE", "EDIT"}
var C = &config{
	RequireInvite:              true,
	RequireActivation:          false,
	ActivationExpiry:           2 * 60 * 60,
	EmailCooldown:              5 * 60,
	EmailPort:                  25,
	ImageBackend:               string(FileBackend),
	PHashDistance:              0,
	VoteApplicationThreshold:   3,
	VotePromotionThreshold:     10,
	VoteCronInterval:           "5m",
	VotingPeriod:               345600,
	MinDestructiveVotingPeriod: 172800,
	DraftTimeLimit:             86400,
	EditUpdateLimit:            1,
	// Sentinel, not a level: see GetEditUpdateMinTrustLevel. -1 means "only the
	// creator", which is this fork's behaviour before #708 and the safe default
	// for an instance that upgrades without reading the note.
	EditUpdateMinTrustLevel: -1,
	RequireSceneDraft:       false,
	RequireTagRole:          false,
	ModAuditRetentionDays:   30,
}

func GetDatabasePath() string {
	return C.Database
}

func GetHost() string {
	return C.Host
}

func GetPort() int {
	return C.Port
}

func GetProfilerPort() *int {
	if C.ProfilerPort == 0 {
		return nil
	}
	return &C.ProfilerPort
}

func GetJWTSignKey() []byte {
	return []byte(C.JWTSignKey)
}

func GetSessionStoreKey() []byte {
	return []byte(C.SessionStoreKey)
}

func GetHTTPUpgrade() bool {
	return C.HTTPUpgrade
}

func GetIsProduction() bool {
	return C.IsProduction
}

// GetRequireInvite returns true if new users cannot register without an invite
// key.
func GetRequireInvite() bool {
	return C.RequireInvite
}

// GetRequireActivation returns true if new users must validate their email address
// via activation to create an account.
func GetRequireActivation() bool {
	return C.RequireActivation
}

// GetActivationExpiry returns the duration before an activation email expires.
func GetActivationExpiry() time.Duration {
	return time.Duration(C.ActivationExpiry * int(time.Second))
}

// GetEmailCooldown returns the duration before a second activation email may
// be generated.
func GetEmailCooldown() time.Duration {
	return time.Duration(C.EmailCooldown * int(time.Second))
}

// GetDefaultUserRoles returns the default roles assigned to a new user
// when created via registration.
func GetDefaultUserRoles() []string {
	if len(C.DefaultUserRoles) == 0 {
		return defaultUserRoles
	}
	return C.DefaultUserRoles
}

func GetEmailHost() string {
	return C.EmailHost
}

func GetEmailPort() int {
	return C.EmailPort
}

func GetEmailUser() string {
	return C.EmailUser
}

func GetEmailPassword() string {
	return C.EmailPW
}

func GetEmailFrom() string {
	return C.EmailFrom
}

// GetEmailTLSMode returns the configured transport security mode for the SMTP
// client.
//
// Recognized values:
//
//	"mandatory"    STARTTLS is required (default).
//	"opportunistic" use STARTTLS if the server offers it, otherwise plaintext.
//	"implicit"     implicit TLS (SMTPS, RFC 8314) -- the connection is wrapped
//	               in TLS before any SMTP command. Required by port 465, which
//	               does not speak STARTTLS.
//	"none"         plaintext, no encryption.
//
// Anything else falls back to "mandatory" to preserve secure-by-default
// behavior.
func GetEmailTLSMode() string {
	switch C.EmailTLSMode {
	case "opportunistic", "none", "implicit":
		return C.EmailTLSMode
	default:
		return "mandatory"
	}
}

func GetHostURL() string {
	return C.HostURL
}

func GetGuidelinesURL() string {
	return C.GuidelinesURL
}

// GetImageLocation returns the path of where to locally store images.
func GetImageLocation() string {
	return C.ImageLocation
}

// GetImageBackend returns the backend used to store images.
func GetImageBackend() ImageBackendType {
	return ImageBackendType(C.ImageBackend)
}

func GetS3Config() *S3Config {
	return &C.S3.S3Config
}

func GetImageResizeConfig() *ImageResizeConfig {
	return &C.Image_Resizing.ImageResizeConfig
}

func GetOTelConfig() *OTelConfig {
	if C.OTel.Endpoint != "" {
		return &C.OTel.OTelConfig
	}
	return nil
}

func GetAutocertConfig() *AutocertConfig {
	if C.Autocert.Enabled {
		return &C.Autocert.AutocertConfig
	}
	return nil
}

func GetMissingAutocertSettings() []string {
	if !C.Autocert.Enabled {
		return nil
	}

	missing := []string{}
	if C.Autocert.Domain == "" {
		missing = append(missing, "domain")
	}
	if C.Autocert.Email == "" {
		missing = append(missing, "email")
	}
	if C.Autocert.CacheDir == "" {
		missing = append(missing, "cache_dir")
	}

	return missing
}

// ValidateImageLocation returns an error is image_location is not set.
func ValidateImageLocation() error {
	if C.ImageLocation == "" {
		return errors.New("ImageLocation not set")
	}

	return nil
}

func GetImageMaxSize() *int {
	size := C.ImageMaxSize
	if size == 0 {
		return nil
	}
	return &size
}

func GetImageJpegQuality() int {
	if C.ImageJpegQuality <= 0 || C.ImageJpegQuality > 100 {
		return 75
	}
	return C.ImageJpegQuality
}

// GetLogFile returns the filename of the file to output logs to.
// An empty string means that file logging will be disabled.
func GetLogFile() string {
	return C.LogFile
}

// GetUserLogFile returns the filename of the file to output user operation
// logs to.
// An empty string means that user operation logging will be output to stderr.
func GetUserLogFile() string {
	return C.UserLogFile
}

// GetLogOut returns true if logging should be output to the terminal
// in addition to writing to a log file. Logging will be output to the
// terminal if file logging is disabled. Defaults to true.
func GetLogOut() bool {
	return C.LogOut
}

// GetLogLevel returns the lowest log level to write to the log.
// Should be one of "Debug", "Info", "Warning", "Error"
func GetLogLevel() string {
	const defaultValue = "Info"

	value := C.LogLevel
	if value != "Debug" && value != "Info" && value != "Warning" && value != "Error" {
		value = defaultValue
	}

	return value
}

func GetPHashDistance() int {
	return C.PHashDistance
}

func InitializeDefaults() error {
	// generate some api keys
	const apiKeyLength = 32

	if viper.GetString(JWTSignKey) == "" {
		signKey, err := utils.GenerateRandomKey(apiKeyLength)
		if err != nil {
			return err
		}
		viper.Set(JWTSignKey, signKey)
	}

	if viper.GetString(SessionStoreKey) == "" {
		sessionStoreKey, err := utils.GenerateRandomKey(apiKeyLength)
		if err != nil {
			return err
		}
		viper.Set(SessionStoreKey, sessionStoreKey)
	}

	if viper.GetString(Database) == "" {
		viper.Set(Database, GetDefaultDatabaseFilePath())
	}

	return viper.WriteConfig()
}

// Unmarshal config
func Initialize() error {
	return viper.Unmarshal(&C)
}

// SetEmailSettingsForTest overrides the SMTP connection settings for the
// duration of a test. It exists because C is unexported and the email package
// cannot configure the manager it drives from a test otherwise; there is no
// other production caller.
//
// Passing a restore function keeps callers from having to snapshot C themselves.
func SetEmailSettingsForTest(host string, port int, user, pw, from, tlsMode string) func() {
	prevHost, prevPort := C.EmailHost, C.EmailPort
	prevUser, prevPW := C.EmailUser, C.EmailPW
	prevFrom, prevTLS := C.EmailFrom, C.EmailTLSMode

	C.EmailHost = host
	C.EmailPort = port
	C.EmailUser = user
	C.EmailPW = pw
	C.EmailFrom = from
	C.EmailTLSMode = tlsMode

	return func() {
		C.EmailHost, C.EmailPort = prevHost, prevPort
		C.EmailUser, C.EmailPW = prevUser, prevPW
		C.EmailFrom, C.EmailTLSMode = prevFrom, prevTLS
	}
}

// SetEmailCooldownForTest sets the email cooldown and returns a function that
// restores the previous value.
//
// Follows the same shape as SetEmailSettingsForTest: the caller holds the
// returned func and defers it, so a test cannot leak its setting into the next
// one. The cooldown is a time.Duration in the getter but a count of seconds in
// the config struct, so the conversion happens here rather than in every caller.
func SetEmailCooldownForTest(d time.Duration) func() {
	prev := C.EmailCooldown
	C.EmailCooldown = int(d.Seconds())
	return func() { C.EmailCooldown = prev }
}

// SetVotePromotionThresholdForTest overrides the vote promotion threshold for
// the duration of a test, returning a func that restores the previous value.
//
// It exists because internal/config has no other way to change this, and the
// promotion path in internal/service/edit is guarded by it. The restore func
// keeps one test from leaking the value into the next, which would otherwise
// make the promotion tests order-dependent.
func SetVotePromotionThresholdForTest(n int) func() {
	prev := C.VotePromotionThreshold
	C.VotePromotionThreshold = n
	return func() { C.VotePromotionThreshold = prev }
}

// SetImageLocationForTest overrides image_location for the duration of a test
// and returns a restore function.
//
// Like SetEmailSettingsForTest, this exists only because C is unexported and
// the storage package has to exercise config-dependent behaviour from a test.
// C.ImageLocation is empty in the test environment, which is precisely the
// condition #649 is about, so a test that could not set it could not cover the
// bug. There is no production caller.
func SetImageLocationForTest(loc string) func() {
	prev := C.ImageLocation
	C.ImageLocation = loc
	return func() { C.ImageLocation = prev }
}

func GetMissingEmailSettings() []string {
	if !GetRequireActivation() {
		return nil
	}

	missing := []string{}
	if GetEmailFrom() == "" {
		missing = append(missing, "EmailFrom")
	}
	if GetEmailHost() == "" {
		missing = append(missing, "EmailHost")
	}
	if GetHostURL() == "" {
		missing = append(missing, "HostURL")
	}

	return missing
}

func GetVotePromotionThreshold() *int {
	if C.VotePromotionThreshold == 0 {
		return nil
	}
	return &C.VotePromotionThreshold
}

func GetVoteApplicationThreshold() int {
	return C.VoteApplicationThreshold
}

func GetVotingPeriod() int {
	return C.VotingPeriod
}

func GetMinDestructiveVotingPeriod() int {
	return C.MinDestructiveVotingPeriod
}

func GetVoteCronInterval() string {
	return C.VoteCronInterval
}

func GetEditUpdateLimit() int {
	return C.EditUpdateLimit
}

// GetEditUpdateMinTrustLevel returns the trust level required to update another
// user's edit, or -1 when only the creator may.
//
// THE SENTINEL IS WHY THE KNOB IS NOT A PLAIN THRESHOLD. Trust levels here are
// 0..5 (Public..Steward) and a plain `level >= n` would make the answer to
// "level 0, minimum 0" mean "every anonymous visitor may rewrite moderation
// history", which is almost certainly not what an operator setting it to 0
// intends and is the kind of misconfiguration that is only discovered by an
// incident. So:
//
//	-1  only the creator  (default; pre-#708 behaviour)
//	 0  equivalent to -1   -- clamped, see clampMinTrustLevel
//	 1  Registered and above, plus ADMIN
//	 5  Steward and above, plus ADMIN
//
// ADMIN ALWAYS PASSES, at every setting including -1 is FALSE. Admin is a ROLE,
// not a trust level -- the two are different axes (auth.RoleEnumAdmin versus
// trust.LevelEnum) and an admin with no trust rollup reads as LevelPublic. So the
// admin check is separate and unconditional, which is what makes upstream's #708
// behaviour a strict subset of this one.
func GetEditUpdateMinTrustLevel() int {
	return ClampEditUpdateMinTrustLevel(C.EditUpdateMinTrustLevel)
}

// ClampEditUpdateMinTrustLevel bounds the configured value to a legal level, and
// is EXPORTED so the decision rule in the edit service cannot reimplement the
// clamp with different boundaries.
//
// The clamp is the point: a config value that names no real level fails CLOSED
// to the creator-only rule rather than open. An operator who types
// `edit_update_min_trust_level: 9` gets the behaviour they had before the knob
// existed, not a comparison against an impossible level. And `0` clamps to
// creator-only rather than meaning "no minimum", because "level 0 >= 0" would
// let every anonymous visitor rewrite moderation history -- which is the kind of
// mistake that is only discovered by an incident.
//
// 0 is the one genuinely surprising clamp, so it is the one worth being explicit
// about: there is no way to configure "everyone". If an operator wants that, the
// answer is not a trust threshold.
func ClampEditUpdateMinTrustLevel(v int) int {
	if v < 1 {
		// Catches both -1 (the explicit creator-only sentinel) and 0 (which is
		// LevelPublic, and must not mean "no minimum").
		return -1
	}
	if v > int(trust.LevelSteward) {
		return int(trust.LevelSteward)
	}
	return v
}

func GetRequireSceneDraft() bool {
	return C.RequireSceneDraft
}

func GetRequireTagRole() bool {
	return C.RequireTagRole
}

func GetTitle() string {
	if C.Title == "" {
		return "Stash-Box"
	}
	return C.Title
}

func GetFrontends() []FrontendConfig {
	return C.Frontends
}

func GetFaviconPath() (*string, error) {
	if len(C.FaviconPath) == 0 {
		return nil, errors.New("favicon_path not set")
	}
	return &C.FaviconPath, nil
}

func GetDraftTimeLimit() int {
	return C.DraftTimeLimit
}

func GetModAuditRetentionDays() int {
	return C.ModAuditRetentionDays
}

func GetMaxOpenConns() int {
	if C.Postgres.MaxOpenConns == 0 {
		return 25
	}
	return C.Postgres.MaxOpenConns
}

func GetMaxIdleConns() int {
	if C.Postgres.MaxIdleConns == 0 {
		return 10
	}
	return C.Postgres.MaxIdleConns
}

func GetConnMaxLifetime() int {
	return C.Postgres.MaxIdleConns
}

func GetCSP() string {
	return C.CSP
}
