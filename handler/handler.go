// Package handler is for all the application handlers and routing
package handler

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gilcrest/go-api-basic/domain/auth"
	"github.com/gilcrest/go-api-basic/domain/errs"
	"github.com/justinas/alice"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"
)

// Handlers is a bundled set of all the application's HTTP handlers
// and HandlerFuncs
type Handlers struct {
	CreateMovieHandler   CreateMovieHandler
	FindMovieByIDHandler FindMovieByIDHandler
	FindAllMoviesHandler FindAllMoviesHandler
	UpdateMovieHandler   UpdateMovieHandler
	DeleteMovieHandler   DeleteMovieHandler
	PingHandler          PingHandler
}

// LoggerHandlerChain returns a handler chain (via alice.Chain)
// initialized with all the standard handlers for logging. The logger
// will be added to the request context for subsequent use with pre-populated
// fields, including the request method, url, status, size, duration, remote IP,
// user agent, referer. A unique Request ID is also added to the logger, context
// and response headers.
func LoggerHandlerChain(logger zerolog.Logger, c alice.Chain) alice.Chain {

	// Install the logger handler with default output on the console
	c = c.Append(hlog.NewHandler(logger))

	// Install extra handler to set request's context fields.
	// Thanks to that handler, all our logs will come with some pre-populated fields.
	c = c.Append(hlog.AccessHandler(func(r *http.Request, status, size int, duration time.Duration) {
		hlog.FromRequest(r).Info().
			Str("method", r.Method).
			Stringer("url", r.URL).
			Int("status", status).
			Int("size", size).
			Dur("duration", duration).
			Msg("")
	})).
		Append(hlog.RemoteAddrHandler("remote_ip")).
		Append(hlog.UserAgentHandler("user_agent")).
		Append(hlog.RefererHandler("referer")).
		Append(hlog.RequestIDHandler("request_id", "Request-Id"))

	return c
}

// JSONContentTypeHandler middleware is used to add the application/json
// Content-Type Header for responses
func JSONContentTypeHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Content-Type", "application/json")
			h.ServeHTTP(w, r) // call original
		})
}

// AccessTokenHandler middleware is used to pull the Bearer token
// from the Authorization header and set it to the request context
// as an auth.AccessToken
func AccessTokenHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			logger := *hlog.FromRequest(r)
			var token string

			// retrieve the context from the http.Request
			ctx := r.Context()

			// Pull the token from the Authorization header
			// by retrieving the value from the Header map with
			// "Authorization" as the key
			// format: Authorization: Bearer
			headerValue, ok := r.Header["Authorization"]
			if ok && len(headerValue) >= 1 {
				token = headerValue[0]
				token = strings.TrimPrefix(token, auth.BearerTokenType+" ")
			}

			// If the token is empty...
			if token == "" {
				// For Unauthenticated and Unauthorized errors,
				// the response body should be empty. Use logger
				// to log the error and then just send
				// http.StatusUnauthorized (401) or http.StatusForbidden (403)
				// depending on the circumstances. "In summary, a
				// 401 Unauthorized response should be used for missing or bad authentication,
				// and a 403 Forbidden response should be used afterwards, when the user is
				// authenticated but isn’t authorized to perform the requested operation on
				// the given resource."
				errs.HTTPErrorResponse(w, logger, errs.E(errs.Unauthenticated, errors.New("Unauthenticated - empty Bearer token")))
				return
			}

			// add access token to context
			ctx = auth.SetAccessToken2Context(ctx, token, auth.BearerTokenType)

			// call original, adding access token to request context
			h.ServeHTTP(w, r.WithContext(ctx))
		})
}

// StandardResponse is meant to be included in all non-error
// response bodies and includes "standard" response fields
type StandardResponse struct {
	Path      string      `json:"path,omitempty"`
	RequestID string      `json:"request_id,omitempty"`
	Data      interface{} `json:"data"`
}

// NewStandardResponse is an initializer for the StandardResponse struct
func NewStandardResponse(r *http.Request, d interface{}) (*StandardResponse, error) {
	var sr StandardResponse
	sr.Path = r.URL.EscapedPath()
	// gets Trace ID from request
	id, ok := hlog.IDFromRequest(r)
	if !ok {
		return nil, errs.E(errors.New("request ID not properly set to request context"))
	}
	sr.RequestID = id.String()

	sr.Data = d

	return &sr, nil
}

// DecoderErr handles an error returned by json.NewDecoder(r.Body).Decode(&data)
// this function will determine the appropriate error response
func DecoderErr(err error) error {
	switch {
	// If the request body is empty (io.EOF)
	// return an error
	case err == io.EOF:
		return errs.E(errs.InvalidRequest, errors.New("Request Body cannot be empty"))
	// If the request body has malformed JSON (io.ErrUnexpectedEOF)
	// return an error
	case err == io.ErrUnexpectedEOF:
		return errs.E(errs.InvalidRequest, errors.New("Malformed JSON"))
	// return all other errors
	case err != nil:
		return errs.E(err)
	}
	return nil
}
