package api

import (
	"context"
	"crypto/tls"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/klauspost/compress/flate"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/ravilushqa/otelgqlgen"

	"github.com/99designs/gqlgen/graphql"
	gqlHandler "github.com/99designs/gqlgen/graphql/handler"
	gqlExtension "github.com/99designs/gqlgen/graphql/handler/extension"
	gqlTransport "github.com/99designs/gqlgen/graphql/handler/transport"
	gqlPlayground "github.com/99designs/gqlgen/graphql/playground"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	"github.com/rs/cors"
	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/autocert"
	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/dataloader"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/ratelimit"
	"github.com/stashapp/stash-box/internal/service"
	"github.com/stashapp/stash-box/internal/service/federation"
	"github.com/stashapp/stash-box/internal/service/mediaplane"
	"github.com/stashapp/stash-box/internal/service/monetization"
	"github.com/stashapp/stash-box/internal/service/user"
	"github.com/stashapp/stash-box/pkg/logger"
)

var version string
var buildstamp string
var githash string
var buildtype string

const APIKeyHeader = "ApiKey"

func getUserAndRoles(ctx context.Context, fac service.Factory, userID string) (*auth.AuthUser, []models.RoleEnum, error) {
	if userID == "" {
		return nil, nil, nil
	}
	id, err := uuid.FromString(userID)
	if err != nil {
		return nil, nil, err
	}
	if u, roles, ok := auth.CacheGet(id); ok {
		return u, roles, nil
	}
	u, roles, err := fac.User().FindWithRoles(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	au := auth.FromUser(u)
	auth.CacheSet(au, roles)
	return au, roles, nil
}

func authenticateHandler(fac service.Factory) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			if origin := r.Header.Get("Origin"); origin != "" {
				if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
					span.SetAttributes(attribute.String("http.request.header.origin", origin))
				}
			}

			// translate api key into current user, if present
			userID := ""
			apiKey := r.Header.Get(APIKeyHeader)
			var err error
			if apiKey != "" {
				userID, err = user.GetUserIDFromAPIKey(apiKey)
			} else {
				// handle session
				userID, err = getSessionUserID(w, r)
			}

			var u *auth.AuthUser
			var roles []models.RoleEnum
			if err == nil {
				u, roles, err = getUserAndRoles(ctx, fac, userID)
			}

			if err != nil {
				logger.Debugf("auth rejected: %v", err)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			// ensure api key of the user matches the passed one
			if apiKey != "" && u != nil && u.APIKey != apiKey {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			// TODO - increment api key counters

			ctx = context.WithValue(ctx, auth.ContextUser, u)
			ctx = context.WithValue(ctx, auth.ContextRoles, roles)

			span := trace.SpanFromContext(ctx)
			if span.SpanContext().IsValid() && u != nil {
				span.SetAttributes(
					attribute.String("user.id", u.ID.String()),
					attribute.String("user.name", u.Name),
				)
			}

			r = r.WithContext(ctx)

			next.ServeHTTP(w, r)
		})
	}
}

func redirect(w http.ResponseWriter, req *http.Request) {
	target := "https://" + req.Host + req.URL.Path
	if len(req.URL.RawQuery) > 0 {
		target += "?" + req.URL.RawQuery
	}
	http.Redirect(w, req, target, http.StatusPermanentRedirect)
}

func Start(fac service.Factory, ui embed.FS) {
	r := chi.NewRouter()
	r.Use(otelchi.Middleware("", otelchi.WithChiRoutes(r)))

	var corsConfig *cors.Cors
	if config.GetIsProduction() {
		corsConfig = cors.AllowAll()
	} else {
		corsConfig = cors.New(cors.Options{
			AllowOriginFunc:  func(origin string) bool { return true },
			AllowCredentials: true,
			AllowedHeaders:   []string{"*"},
		})
	}

	r.Use(corsConfig.Handler)
	r.Use(authenticateHandler(fac))
	r.Use(middleware.Recoverer)

	compressor := middleware.NewCompressor(flate.DefaultCompression)
	r.Use(compressor.Handler)
	r.Use(middleware.StripSlashes)
	r.Use(BaseURLMiddleware)

	recoverFunc := func(ctx context.Context, err any) error {
		logger.Error(err)
		debug.PrintStack()

		message := fmt.Sprintf("Internal system error. Error <%v>", err)
		return errors.New(message)
	}

	gqlConfig := models.Config{
		Resolvers: NewResolver(fac),
		Directives: models.DirectiveRoot{
			IsUserOwner: IsUserOwnerDirective,
			HasRole:     HasRoleDirective,
		},
	}
	gqlSrv := gqlHandler.New(models.NewExecutableSchema(gqlConfig))
	gqlSrv.SetRecoverFunc(recoverFunc)
	gqlSrv.AddTransport(gqlTransport.Options{})
	gqlSrv.AddTransport(gqlTransport.GET{})
	gqlSrv.AddTransport(gqlTransport.POST{})
	gqlSrv.AddTransport(gqlTransport.MultipartForm{})
	gqlSrv.Use(gqlExtension.Introspection{})
	gqlSrv.Use(otelgqlgen.Middleware(otelgqlgen.WithCreateSpanFromFields(func(*graphql.FieldContext) bool { return false })))
	gqlSrv.SetErrorPresenter(func(ctx context.Context, e error) *gqlerror.Error {
		err := graphql.DefaultErrorPresenter(ctx, e)

		// Get username from context if available
		username := "anonymous"
		if user := auth.GetCurrentUser(ctx); user != nil {
			username = user.Name
		}

		// Log the error at debug level with username
		logger.Debugf("GraphQL error [user: %s]: %v", username, err)

		return err
	})

	// THE PUBLIC SURFACE, and the limit is mounted on it rather than on the router so that nothing else is
	// counted: the SPA, the assets and the invitation landing page are not a caller spending an API budget,
	// and a limit that covered them would be felt by a member loading a page.
	// TWO LIMITS, ONE COUNTING RULE (plan phase 15, spec §18). The premium configuration is reachable only
	// through the predicate, which is monetization's answer to "does this credential belong to a comped
	// member" -- so with monetization off, or the premium subscription stream off, NO credential is premium
	// and this is the single-limit limiter it always was. The predicate reads the API key's own member id out
	// of the signed key, so it costs no query per request, and a credential it cannot parse is a free caller
	// rather than an error.
	freeLimit, premiumLimit := monetization.Limits()
	limiter := ratelimit.NewWithTiers(freeLimit, premiumLimit, monetization.CredentialIsPremium)
	r.Handle("/graphql", ratelimit.Middleware(limiter, ratelimit.Credential)(dataloader.Middleware(fac)(gqlSrv)))

	// The federation identify endpoint (phase 12, slice 12b). It is registered OUTSIDE the rate limiter
	// and without the GraphQL dataloader: a peer has no account here, so a per-credential limiter would
	// bucket every peer under one anonymous key, and the dataloader is a per-session batching device for
	// a graph a peer does not walk. What it does run is the authenticate middleware (which tolerates an
	// anonymous request, exactly as /graphql does), the compressor and the recoverer.
	//
	// The path comes from the federation package rather than a literal here, because it is the same
	// constant the outbound client builds its URL from -- one name, two sides.
	r.Post(federation.AnswerPath, FederationIdentify(fac))

	// The tag table (phase 12, slice 12e), registered beside the answer because it is the same kind of
	// surface: a peer-readable statement about what this instance speaks. Outside the rate limiter for the
	// same reason the answer is -- the limiter is keyed on this instance's credentials and a peer has
	// none, so putting it inside would charge a peer for somebody else's account.
	r.Get(federation.TagsPath, FederationTags())

	if !config.GetIsProduction() {
		r.Handle("/playground", gqlPlayground.Handler("GraphQL playground", "/graphql"))
	}

	mountedPrefixes := make(map[string]bool)
	for _, fe := range config.GetFrontends() {
		if fe.Path == "" || fe.Prefix == "" || fe.Prefix == "/" {
			logger.Warnf("skipping frontend with missing path/prefix or reserved prefix: %+v", fe)
			continue
		}
		if mountedPrefixes[fe.Prefix] {
			logger.Warnf("skipping frontend with duplicate prefix %s", fe.Prefix)
			continue
		}
		mountedPrefixes[fe.Prefix] = true
		r.Mount(fe.Prefix, frontendRoutes{dir: fe.Path, prefix: fe.Prefix}.Routes())
		logger.Infof("serving frontend from %s at %s", fe.Path, fe.Prefix)
	}

	r.Mount("/", rootRoutes{ui: ui, limiter: limiter}.Routes(fac))

	if config.GetProfilerPort() != nil {
		go func() {
			mux := http.NewServeMux()
			mux.HandleFunc("/", pprof.Index)
			mux.HandleFunc("/cmdline", pprof.Cmdline)
			mux.HandleFunc("/profile", pprof.Profile)
			mux.HandleFunc("/symbol", pprof.Symbol)
			mux.HandleFunc("/trace", pprof.Trace)
			mux.Handle("/allocs", pprof.Handler("allocs"))
			mux.Handle("/block", pprof.Handler("block"))
			mux.Handle("/goroutine", pprof.Handler("goroutine"))
			mux.Handle("/heap", pprof.Handler("heap"))
			mux.Handle("/mutex", pprof.Handler("mutex"))
			mux.Handle("/threadcreate", pprof.Handler("threadcreate"))
			logger.Infof("profiler is running at http://localhost:%d/", *config.GetProfilerPort())
			logger.Fatal(http.ListenAndServe(":"+strconv.Itoa(*config.GetProfilerPort()), mux))
		}()
	}

	address := config.GetHost() + ":" + strconv.Itoa(config.GetPort())

	// Priority 1: Autocert (Let's Encrypt)
	if tlsConfig := autocert.Init(); tlsConfig != nil {
		httpsServer := &http.Server{
			Addr:      address,
			Handler:   r,
			TLSConfig: tlsConfig,
		}

		// Start HTTP server for ACME HTTP-01 challenges on port 80
		// Non-challenge requests are redirected to HTTPS
		go func() {
			logger.Infof("Starting HTTP server on %s:80 for ACME challenges", config.GetHost())
			if err := http.ListenAndServe(config.GetHost()+":80", autocert.HTTPHandler(http.HandlerFunc(redirect))); err != nil {
				logger.Errorf("HTTP server error: %v", err)
			}
		}()

		go func() {
			printVersion()
			logPosture()
			logger.Infof("stash-box is running on HTTPS (autocert) at https://%s/", address)
			logger.Fatal(httpsServer.ListenAndServeTLS("", ""))
		}()
		return
	}

	// Priority 2: File-based TLS
	if tlsConfig := makeTLSConfig(); tlsConfig != nil {
		httpsServer := &http.Server{
			Addr:      address,
			Handler:   r,
			TLSConfig: tlsConfig,
		}

		if config.GetHTTPUpgrade() {
			go func() {
				logger.Fatal(http.ListenAndServe(config.GetHost()+":80", http.HandlerFunc(redirect)))
			}()
		}

		go func() {
			printVersion()
			logPosture()
			logger.Infof("stash-box is running on HTTPS at https://%s/", address)
			logger.Fatal(httpsServer.ListenAndServeTLS("", ""))
		}()
		return
	}

	// Priority 3: HTTP only
	server := &http.Server{
		Addr:    address,
		Handler: r,
	}

	go func() {
		printVersion()
		logPosture()
		logger.Infof("stash-box is running on HTTP at http://%s/", address)
		logger.Fatal(server.ListenAndServe())
	}()
}

func printVersion() {
	versionString := version
	if buildtype != "OFFICIAL" {
		versionString += " (" + githash + ")"
	}
	fmt.Printf("stash-box version: %s - %s\n", versionString, buildstamp)
}

// logPosture states this process's content postures in the log, once, at startup.
//
// WHY IT IS LOGGED RATHER THAN DOCUMENTED. Both switches are off by default, so an operator's
// question is never "what do these do" but "which posture is THIS instance running" -- and the
// answer has to be in the place they are already looking, next to the version, without a config
// file to grep. It is also what makes the switch READ rather than merely parsed: a key that a
// process never mentions is a key nobody can tell was ignored, which is the failure mode this
// repository has already shipped once (a rung that wrote a switch no code read).
//
// The content-fetch half is logged by internal/cron, because that is where its jobs are registered
// or (when the switch is off) not registered at all; the media-plane half is logged here because
// there is nothing to register yet -- the capability is specified and gated, and this line is the
// only runtime evidence of which posture the instance is in.
func logPosture() {
	logger.Infof("%s", mediaplane.PostureLine())
	// The monetization line states the switch, the streams and the always-free list's enforcement in code; the
	// rate-limit line follows it and states BOTH limits when an entitlement exists, so the two lines together
	// answer "what does this instance charge for, and how fast may a paying caller go".
	logger.Infof("%s", monetization.PostureLine())
	logger.Infof("%s", ratelimit.PostureLine(ratelimit.NewWithTiers(
		config.GetRateLimit(), config.GetMonetizationPremiumRateLimit(), monetization.CredentialIsPremium)))
}

func GetVersion() (string, string, string) {
	return version, githash, buildstamp
}

func makeTLSConfig() *tls.Config {
	cert, err := os.ReadFile(config.GetSSLCert())
	if err != nil {
		return nil
	}

	key, err := os.ReadFile(config.GetSSLKey())
	if err != nil {
		return nil
	}

	certs := make([]tls.Certificate, 1)
	certs[0], err = tls.X509KeyPair(cert, key)
	if err != nil {
		return nil
	}
	tlsConfig := &tls.Config{
		Certificates: certs,
		MinVersion:   tls.VersionTLS13,
	}

	return tlsConfig
}

type contextKey struct {
	name string
}

var (
	BaseURLCtxKey = &contextKey{"BaseURL"}
)

func BaseURLMiddleware(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var scheme string
		if strings.Compare("https", r.URL.Scheme) == 0 || r.Proto == "HTTP/2.0" || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		} else {
			scheme = "http"
		}
		baseURL := scheme + "://" + r.Host

		r = r.WithContext(context.WithValue(ctx, BaseURLCtxKey, baseURL))

		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}
