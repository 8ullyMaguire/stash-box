package config

import (
	"errors"
	"time"

	"github.com/spf13/viper"

	"github.com/stashapp/stash-box/internal/ratelimit"
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
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`

	// InstanceID is what this instance calls ITSELF on the federation wire (phase 12, slice 12b).
	//
	// It is not derived from the hostname and has no default: an instance that cannot say who it is
	// cannot answer a peer, because the peer registry is keyed by instance id and an answer that does
	// not name its sender is an answer nobody can attribute. Empty means unset, and the answer endpoint
	// REFUSES with the key's name rather than inventing an identity -- the same posture `content_delivery`
	// takes about its own key.
	InstanceID string `mapstructure:"instance_id"`

	// TagMappings is this instance's declaration of what its LOCAL tag names mean in the agreed core
	// vocabulary (phase 12, slice 12e): `{"big-breasts": "measurements"}`. It lives in config because it
	// is the operator's statement about their own vocabulary -- not a property of the data, not
	// something to infer, and not something this program can discover on its own.
	TagMappings  map[string]string `mapstructure:"tag_mappings"`
	Database     string            `mapstructure:"database"`
	ProfilerPort int               `mapstructure:"profiler_port"`

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
	// ContentSweepInterval is how often the health sweep looks at content sources. A knob because
	// every tick makes third-party HTTP requests, so the cost is proportional to how many sources
	// this instance tracks.
	ContentSweepInterval string `mapstructure:"content_sweep_interval"`
	// ContentFetchSources lets this instance make outbound HTTP requests to third-party content
	// sources: the ingest pipeline's page fetch, the health sweep's HEAD request and Wayback
	// "Save Page Now" submissions.
	//
	// OFF BY DEFAULT, and that is a legal posture and not a cautious default. An instance that
	// fetches third-party pages is a scraper, and an instance that then serves what it fetched is
	// a host; this programme builds a catalogue of references -- claims about works and the
	// addresses of pages -- so the behaviour that could make it the second thing is turned on
	// deliberately, by an operator who knows what it means. When it is off, the cron jobs that
	// would make those requests are not registered at all (internal/cron) and the service entry
	// points refuse by name (internal/service/content/policy.go), so "this instance makes no
	// outbound request" is a property and not a promise.
	//
	// See docs/spec/feature-content-aggregator.md's respec section (2026-10-15) for what it does
	// NOT cover, so nobody assumes it covers more than it does.
	ContentFetchSources bool `mapstructure:"content_fetch_sources"`
	// ContentFetchMinInterval is the least time between two requests this instance makes to the
	// SAME host, and the pace part of not being a scraper.
	//
	// A knob because a bulk import by an operator who owns the site they are importing should not
	// have to wait two seconds per page, and because the honest default is a polite one rather than
	// a fast one: a single instance knocking on one host as fast as its queue drains is
	// indistinguishable from a scraper to the host, and the host's response to that is a block --
	// which would make the feature useless before any legal question arose.
	//
	// THE OTHER HALF OF THIS POSTURE IS NOT A KNOB. robots.txt is honoured unconditionally
	// (internal/service/hostrules), because a switch whose only use is to ignore a host's stated
	// terms is a switch that turns this instance into the category of client the whole respec exists
	// to keep it out of.
	ContentFetchMinInterval string `mapstructure:"content_fetch_min_interval"`
	// ContentDelivery lets this instance SERVE CONTENT: hosting, the variant ladder, the media
	// toolchain, the players, PWA offline caching and the CDN/edge -- the media plane, phase 10.
	//
	// OFF BY DEFAULT, AND HERE THAT DEFAULT IS THE WHOLE SAFETY ARGUMENT. The media plane was struck
	// from this programme on 2026-10-15 and restored the same day by owner instruction
	// ("include it in the spec again but disable it by default in config"), and what makes the
	// restoration safe is that the earlier refusal was never about the code existing: an instance
	// that SERVES third-party material is a host, and a host is the category of site that is DMCA'd
	// out of existence. So the capability is specified, planned, gated, and off in every shipped
	// config -- and `content_delivery: true` is a posture an operator adopts deliberately, in
	// writing, knowing it changes what this instance IS.
	//
	// THIS COMMENT DELIBERATELY DOES NOT NAME THE TOOLCHAIN OR THE PLAYER ELEMENT. The gate that
	// enforces the posture is a TEXT scan, so a forbidden word in a comment is a hit -- the
	// capability's own module may discuss them, and this file may not. Reword rather than add an
	// exception: an exception list is how the gate stops meaning anything.
	//
	// READ IT THROUGH internal/service/mediaplane's RequireDelivery, never as a bare bool: the
	// switch and the error a caller returns belong together, and the refusal must name the key.
	// docs/spec/feature-content-aggregator.md's second respec section (2026-10-15) records the
	// reversal chain and the guardrails that do NOT move with this switch -- third-party embedding
	// and proxying stay refused, and scripts/check-no-content-plane.py keeps failing the build for
	// them whatever this key says.
	ContentDelivery bool `mapstructure:"content_delivery"`
	// RateLimitRequestsPerMinute is the sustained request rate one credential may make against this
	// instance's public surfaces (plan slice 13d). ZERO MEANS NO LIMIT, and there is no separate switch
	// beside it: the number already says whether there is a limit, and a boolean could only contradict it.
	//
	// ON BY DEFAULT, unlike the content switches above it, and the difference is what each one protects
	// against. Those gate a CAPABILITY an operator adopts deliberately, so the safe default is off. This
	// one protects the instance from a caller that loops -- an accident or a scraper -- and a limit that
	// is off by default protects nobody. It is set high enough (600/minute, burst 120) that no ordinary
	// client reaches it, and a client that does gets the limit named and a Retry-After to obey.
	RateLimitRequestsPerMinute int `mapstructure:"rate_limit_requests_per_minute"`
	// RateLimitBurst is how many requests a credential may make at once, before the sustained rate applies.
	// A page load issues several requests in a moment, so a limit with no burst breaks ordinary use while
	// barely slowing a loop.
	RateLimitBurst int `mapstructure:"rate_limit_burst"`
	// MonetizationEnabled lets this instance CHARGE for the UX features NexusXXX §18.1 lists -- the premium
	// API tier today, and later creator subscriptions, contextual ads, data licensing and a commercial tier
	// (plan phase 15, spec §18).
	//
	// OFF BY DEFAULT, AND OFF IS NOT "FREE FOR ALL": off means NO STREAM MAY BE ON, and a stream that is on
	// while this is false is refused BY NAME rather than ignored -- a configuration that states two
	// contradictory things is one whose operator will believe the wrong half. This fork is MIT, so what an
	// instance charges for is its operator's decision; what the programme owes a reader is that the decision
	// is published (GET /monetization) and that §18.2's always-free list cannot be shortened quietly, which
	// is enforced in code (internal/service/monetization/alwaysfree.go) rather than promised on a page.
	//
	// READ IT THROUGH internal/service/monetization's RequireStream/Gate, never as a bare bool: the switch,
	// the stream list and the refusals belong together, and every refusal names the key or the stream so an
	// operator can act on the message alone.
	MonetizationEnabled bool `mapstructure:"monetization_enabled"`
	// MonetizationStreams are §18.1's revenue streams this operator has switched on, by name. A stream with
	// no implementation on this tree is refused BY NAME (monetization.RequireStream) so that "monetization is
	// enabled" cannot be a claim about code that does not exist: the premium subscription is implemented;
	// creator_subs, ads, data_licensing and commercial_api_tier are named, and each refusal says what it
	// would take to build.
	MonetizationStreams []string `mapstructure:"monetization_streams"`
	// MonetizationPremiumMembers are the member ids this operator has comped premium. Slice a's only grant
	// mechanism, and deliberately a real one: a stored grant would have no writer until §18.3's reward pass
	// exists (slice b), and a column nothing writes is indistinguishable from a bug. When slice b lands this
	// key is REFUSED by name, so the file and the table cannot both claim to be the source of truth about
	// who pays.
	MonetizationPremiumMembers []string `mapstructure:"monetization_premium_members"`
	// MonetizationPremiumRequestsPerMinute is the sustained API rate one PREMIUM credential may make, against
	// RateLimitRequestsPerMinute for everybody else (§18.1, "higher API limits"). Refused at startup when the
	// premium_subscription stream is on and this is not ABOVE the free limit: a "higher limit" that is not higher
	// is a promise nothing honours.
	MonetizationPremiumRequestsPerMinute int `mapstructure:"monetization_premium_requests_per_minute"`
	// MonetizationPremiumBurst is the premium tier's burst, for the same reason RateLimitBurst exists, and
	// validated the same way: a premium tier a page load can exhaust is not a benefit.
	MonetizationPremiumBurst int `mapstructure:"monetization_premium_burst"`
	// How often subscribed saved searches are run and their owners notified. One query per
	// subscribed search per tick, so the cost is proportional to how many exist.
	SavedSearchNotifyInterval string `mapstructure:"saved_search_notify_interval"`
	// Which vector store scene similarity re-ranks with: "noop" (the default) or
	// "inmemory". See GetSimilarVectorStore.
	SimilarVectorStore string `mapstructure:"similar_vector_store"`
	// Dimensions of the stored vectors. Ignored by the no-op store.
	SimilarVectorDimensions int `mapstructure:"similar_vector_dimensions"`
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
	// Show the genital attribute fields on performers. Disabled by default. An
	// instance whose content never depicts genitals (e.g. JAV) can leave
	// it off to keep the performer form/data uncluttered.
	EnableGenitalAttributes bool `mapstructure:"enable_genital_attributes"`

	// Email settings
	EmailHost    string `mapstructure:"email_host"`
	EmailPort    int    `mapstructure:"email_port"`
	EmailUser    string `mapstructure:"email_user"`
	EmailPW      string `mapstructure:"email_password"`
	EmailFrom    string `mapstructure:"email_from"`
	EmailTLSMode string `mapstructure:"email_tls_mode"`
	HostURL      string `mapstructure:"host_url"`

	// Image storage settings
	// The address a notice is sent to. Optional in the file and REQUIRED by the published policy: an
	// instance that hosts nothing may run without one, and the policy page says out loud when it is unset
	// (`GET /policy`), because "we answer notices" with no address to send one to is a promise nobody can
	// collect on. Phase 14's ordering makes that concrete: the policy text gates phase 10's first stored
	// byte, and the text is not complete until this is set.
	AbuseContact string `mapstructure:"abuse_contact"`

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
	// Number of days to retain image audit logs (0 to disable logging)
	ImageAuditRetentionDays int `mapstructure:"image_audit_retention_days"`

	CSP string `mapstructure:"csp"`

	// Operator changes to the built-in identification source catalogue (SPEC §8,
	// plan step 7a): each entry either replaces a built-in source by exact name
	// or adds one. Empty leaves the catalogue as DefaultSources ships it, which
	// is the whole point -- the no-network half needs no configuration.
	IdentificationSources []IdentificationSourceConfig `mapstructure:"identification_sources"`
}

// IdentificationSourceConfig is one operator-supplied identification source.
//
// Enabled is a POINTER because "not specified" and "false" are different
// statements: an entry that only changes a template must not silently turn the
// source off, and with a plain bool the destructive reading is the one a missing
// key produces. Reading nil as "unchanged" is what makes the config additive.
type IdentificationSourceConfig struct {
	Name     string `mapstructure:"name"`
	Kind     string `mapstructure:"kind"`
	Home     string `mapstructure:"home"`
	Template string `mapstructure:"template"`
	Enabled  *bool  `mapstructure:"enabled"`
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
	RequireInvite:            true,
	RequireActivation:        false,
	ActivationExpiry:         2 * 60 * 60,
	EmailCooldown:            5 * 60,
	EmailPort:                25,
	ImageBackend:             string(FileBackend),
	PHashDistance:            0,
	VoteApplicationThreshold: 3,
	VotePromotionThreshold:   10,
	VoteCronInterval:         "5m",
	ContentSweepInterval:     "1h",
	// Off, and this is the one default in this file that is a legal posture rather than a
	// cost/behaviour trade-off: it is what decides whether the instance makes outbound requests
	// to third-party content sources at all. See the field's comment.
	ContentFetchSources:     false,
	ContentFetchMinInterval: "2s",
	// Off, and for the same reason as the line above it: serving content is what makes an instance a
	// host. See the field's comment -- the capability is specified and planned, and every shipped
	// config leaves it off.
	ContentDelivery: false,
	// See the field comments: on by default because this one protects the instance rather than gating a
	// capability, and generous because a limit a member can hit while browsing is a bug in the limit.
	RateLimitRequestsPerMinute: 600,
	RateLimitBurst:             120,
	// The premium tier's limit (§18.1 "higher API limits", plan phase 15). Double the free rate and double
	// its burst, because a premium tier that is 10% higher is not a benefit a member would notice -- and
	// these numbers are only reachable when an operator has switched the stream on.
	MonetizationPremiumRequestsPerMinute: 1200,
	MonetizationPremiumBurst:             240,
	SavedSearchNotifyInterval:            "15m",
	// No embedding model produces vectors on this instance, so the default store holds
	// nothing and the re-ranker provably does nothing -- rather than a half-wired store
	// whose emptiness would be indistinguishable from a bug.
	SimilarVectorStore:         "noop",
	SimilarVectorDimensions:    512,
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
	// Upstream #1269: genital attributes are opt-in per instance. The default stays
	// false because the feature is not what every instance wants exposed.
	EnableGenitalAttributes: false,
	ModAuditRetentionDays:   30,
	// Upstream #1215: image label audit retention, same shape as the edit/mod audit
	// retention above it.
	ImageAuditRetentionDays: 30,
}

func GetDatabasePath() string {
	return C.Database
}

func GetHost() string {
	return C.Host
}

// GetInstanceID is this instance's own name on the federation wire, or "" when unset.
//
// An empty string is returned rather than a hostname-derived guess: two instances behind one host
// would derive the same id, and a peer registry keyed on a guess attributes one instance's answers
// to another.
func GetInstanceID() string {
	return C.InstanceID
}

// GetTagMappings is the local-to-core tag declaration, never nil so that a caller resolving tags on an
// instance that declared none gets refusals rather than a nil-map panic.
func GetTagMappings() map[string]string {
	if C.TagMappings == nil {
		return map[string]string{}
	}
	return C.TagMappings
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

// GetAbuseContact returns the address a notice is sent to.
//
// Empty is a legitimate state for an instance that hosts nothing, and the policy page reports it rather
// than hiding it: see the field's own comment, and phase 14's ordering.
func GetAbuseContact() string {
	return C.AbuseContact
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

// GetContentSweepInterval is how often the health sweep runs (docs/spec/feature-content-aggregator.md,
// plan phase 9 slice 9b).
func GetContentSweepInterval() string {
	return C.ContentSweepInterval
}

// GetContentFetchSources reports whether this instance may make outbound HTTP requests to
// third-party content sources (the 2026-10-15 respec). False unless an operator has said
// otherwise, and false in every shipped config file.
//
// ASK THIS THROUGH internal/service/content's RequireFetching RATHER THAN HERE: the switch and the
// error a caller returns belong together, and a caller that reads the bool directly has to invent
// its own message for the refusal.
func GetContentFetchSources() bool {
	return C.ContentFetchSources
}

// GetContentDelivery reports whether this instance may SERVE content (the media plane, plan phase
// 10). False unless an operator has said otherwise, and false in every shipped config file.
//
// ASK THIS THROUGH internal/service/mediaplane's RequireDelivery RATHER THAN HERE, for the same
// reason GetContentFetchSources points at content.RequireFetching: the switch and the refusal
// belong together, and a caller that reads the bool has to invent its own message for the refusal.
func GetContentDelivery() bool {
	return C.ContentDelivery
}

// GetRateLimit is the limit this instance enforces per credential on its public surfaces (plan slice 13d).
// Zero requests per minute, or a zero burst, means no limit -- see the fields' comments for why that is
// expressed as a number rather than a switch.
func GetRateLimit() ratelimit.Config {
	return ratelimit.Config{
		RequestsPerMinute: C.RateLimitRequestsPerMinute,
		Burst:             C.RateLimitBurst,
	}
}

// GetMonetizationEnabled reports whether this instance may CHARGE for §18.1's UX features (plan phase 15).
// False unless an operator has said otherwise, and false in every shipped config file.
//
// ASK THIS THROUGH internal/service/monetization RATHER THAN HERE, for the same reason GetContentDelivery
// points at mediaplane.RequireDelivery: the switch and the refusals belong together, and a caller that reads
// the bool has to invent its own message for "this instance charges for nothing".
func GetMonetizationEnabled() bool {
	return C.MonetizationEnabled
}

// GetMonetizationStreams are the §18.1 streams this operator switched on, by name. Read through
// monetization.RequireStream, which is what refuses an unknown name and a stream with no implementation.
func GetMonetizationStreams() []string {
	return C.MonetizationStreams
}

// GetMonetizationPremiumMembers are the member ids this operator has comped premium (slice a's grant
// mechanism; slice b replaces it with recorded grants and refuses this key).
func GetMonetizationPremiumMembers() []string {
	return C.MonetizationPremiumMembers
}

// GetMonetizationPremiumRateLimit is the limit a PREMIUM credential gets, against GetRateLimit for everybody
// else. It is a ratelimit.Config rather than two ints for the same reason GetRateLimit is: the limiter's
// "zero means off" rule is one decision, and a caller that unpacked these into its own pair would be a second
// place it is stated.
func GetMonetizationPremiumRateLimit() ratelimit.Config {
	return ratelimit.Config{
		RequestsPerMinute: C.MonetizationPremiumRequestsPerMinute,
		Burst:             C.MonetizationPremiumBurst,
	}
}

// GetContentFetchMinInterval is the least time between two requests to the same host
// (internal/service/hostrules). An unparseable or non-positive value falls back to two seconds:
// the knob exists to let an operator go SLOWER or, if they own the host, faster -- never to turn
// pacing off by typo, because "0s" is exactly the setting a scraper wants.
func GetContentFetchMinInterval() time.Duration {
	parsed, err := time.ParseDuration(C.ContentFetchMinInterval)
	if err != nil || parsed <= 0 {
		return 2 * time.Second
	}
	return parsed
}

// GetSavedSearchNotifyInterval returns the interval between saved-search notification runs,
// in the same bare-duration form as the vote interval ("15m"), because the two knobs are read
// by an operator in the same file and a second format would be a trap.
func GetSavedSearchNotifyInterval() string {
	return C.SavedSearchNotifyInterval
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

func GetEnableGenitalAttributes() bool {
	return C.EnableGenitalAttributes
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

func GetImageAuditRetentionDays() int {
	return C.ImageAuditRetentionDays
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
	// This returned `C.Postgres.MaxIdleConns` — the max-idle-conns accessor was cloned and
	// its body never changed. `conn_max_lifetime` was therefore ignored entirely, and with
	// no config file set at all the value was 0, which pgxpool reads as "never recycle a
	// connection". Unbounded lifetimes are what produce a pool handing out dead sockets
	// behind a pgbouncer or a NAT that drops idle connections.
	//
	// The default mirrors GetMaxIdleConns' shape: unset falls back to a real value rather
	// than to the disabling 0. 60 minutes is what upstream proposed and is a sensible
	// recycle point for a long-lived server.
	if C.Postgres.ConnMaxLifetime <= 0 {
		return 60
	}
	return C.Postgres.ConnMaxLifetime
}

func GetCSP() string {
	return C.CSP
}

// GetSimilarVectorStore returns which vector store scene similarity re-ranks with.
//
// "noop" (the default) keeps nothing and returns nothing, so the similarity order is the
// structured evidence's order alone. "inmemory" holds a []float32 per scene and compares
// them by cosine, brute force, over the candidate set the structured queries already
// narrowed.
//
// A NAME is returned rather than a constructed store because the config package builds no
// services: the factory turns this string into an implementation in one place, so an
// unknown value is refused there with the service's own error rather than silently becoming
// the default here.
func GetSimilarVectorStore() string {
	return C.SimilarVectorStore
}

// GetSimilarVectorDimensions returns the width of a stored vector.
//
// 512 because that is what the embedding models this feature is aimed at produce, and
// because a wrong width has to be REFUSED rather than padded: two vectors of different
// lengths come from different models, and comparing them up to the shorter one would produce
// a number. See similar.ErrDimension.
func GetSimilarVectorDimensions() int {
	return C.SimilarVectorDimensions
}
