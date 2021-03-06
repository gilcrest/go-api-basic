package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/gilcrest/go-api-basic/domain/random"

	"github.com/gilcrest/go-api-basic/domain/random/randomtest"

	"github.com/gilcrest/go-api-basic/datastore/moviestore/moviestoretest"

	"github.com/gilcrest/go-api-basic/domain/auth"

	qt "github.com/frankban/quicktest"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/justinas/alice"
	"github.com/rs/zerolog/hlog"

	"github.com/gilcrest/go-api-basic/datastore/datastoretest"
	"github.com/gilcrest/go-api-basic/datastore/moviestore"
	"github.com/gilcrest/go-api-basic/domain/auth/authtest"
	"github.com/gilcrest/go-api-basic/domain/logger"
)

func TestDefaultMovieHandlers_CreateMovie(t *testing.T) {
	t.Run("typical", func(t *testing.T) {
		// set environment variable NO_DB to true if you don't
		// have database connectivity and this test will be skipped
		if os.Getenv("NO_DB") == "true" {
			t.Skip("skipping db dependent test")
		}

		// initialize quickest checker
		c := qt.New(t)

		// initialize a zerolog Logger
		lgr := logger.NewLogger(os.Stdout, true)

		// initialize DefaultDatastore
		ds, cleanup := datastoretest.NewDefaultDatastore(t, lgr)

		// defer cleanup of the database until after the test is completed
		t.Cleanup(cleanup)

		// initialize the DefaultTransactor for the moviestore
		transactor := moviestore.NewDefaultTransactor(ds)

		// initialize the DefaultSelector for the moviestore
		selector := moviestore.NewDefaultSelector(ds)

		// initialize mockAccessTokenConverter
		mockAccessTokenConverter := authtest.NewMockAccessTokenConverter(t)

		// initialize DefaultStringGenerator
		randomStringGenerator := random.DefaultStringGenerator{}

		// initialize DefaultMovieHandlers
		dmh := DefaultMovieHandlers{
			RandomStringGenerator: randomStringGenerator,
			AccessTokenConverter:  mockAccessTokenConverter,
			Authorizer:            authtest.NewMockAuthorizer(t),
			Transactor:            transactor,
			Selector:              selector,
		}

		// setup request body using anonymous struct
		requestBody := struct {
			Title    string `json:"title"`
			Rated    string `json:"rated"`
			Released string `json:"release_date"`
			RunTime  int    `json:"run_time"`
			Director string `json:"director"`
			Writer   string `json:"writer"`
		}{
			Title:    "Repo Man",
			Rated:    "R",
			Released: "1984-03-02T00:00:00Z",
			RunTime:  92,
			Director: "Alex Cox",
			Writer:   "Alex Cox",
		}

		// encode request body into buffer variable
		var buf bytes.Buffer
		err := json.NewEncoder(&buf).Encode(requestBody)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}

		// setup path
		path := pathPrefix + moviesV1PathRoot

		// form request using httptest
		req := httptest.NewRequest(http.MethodPost, path, &buf)

		// add test access token
		req.Header.Add("Authorization", auth.BearerTokenType+" abc123def1")

		// create middleware to extract the request ID from
		// the request context for testing comparison
		var requestID string
		requestIDMiddleware := func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rID, ok := hlog.IDFromRequest(r)
				if !ok {
					t.Fatal("Request ID not set to request context")
				}
				requestID = rID.String()

				h.ServeHTTP(w, r)
			})
		}

		// retrieve createMovieHandler HTTP handler
		createMovieHandler := ProvideCreateMovieHandler(dmh)

		// initialize ResponseRecorder to use with ServeHTTP as it
		// satisfies ResponseWriter interface and records the response
		// for testing
		rr := httptest.NewRecorder()

		// initialize alice Chain to chain middleware
		ac := alice.New()

		// setup full handler chain needed for request
		h := LoggerHandlerChain(lgr, ac).
			Append(AccessTokenHandler).
			Append(JSONContentTypeHandler).
			Append(requestIDMiddleware).
			Then(createMovieHandler)

		// call the handler ServeHTTP method to execute the request
		// and record the response
		h.ServeHTTP(rr, req)

		// Assert that Response Status Code equals 200 (StatusOK)
		c.Assert(rr.Code, qt.Equals, http.StatusOK)

		// createMovieResponse is the response struct for a Movie
		// the response struct is tucked inside the handler, so we
		// have to recreate it here
		type createMovieResponse struct {
			ExternalID      string `json:"external_id"`
			Title           string `json:"title"`
			Rated           string `json:"rated"`
			Released        string `json:"release_date"`
			RunTime         int    `json:"run_time"`
			Director        string `json:"director"`
			Writer          string `json:"writer"`
			CreateUsername  string `json:"create_username"`
			CreateTimestamp string `json:"create_timestamp"`
			UpdateUsername  string `json:"update_username"`
			UpdateTimestamp string `json:"update_timestamp"`
		}

		// standardResponse is the standard response struct used for
		// all response bodies, the Data field is actually an
		// interface{} in the real struct (handler.StandardResponse),
		// but it's easiest to decode to JSON using a proper struct
		// as below
		type standardResponse struct {
			Path      string              `json:"path"`
			RequestID string              `json:"request_id"`
			Data      createMovieResponse `json:"data"`
		}

		// retrieve the mock User that is used for testing
		u, _ := mockAccessTokenConverter.Convert(req.Context(), authtest.NewAccessToken(t))

		// setup the expected response data
		wantBody := standardResponse{
			Path:      path,
			RequestID: requestID,
			Data: createMovieResponse{
				ExternalID:      "superRandomString",
				Title:           "Repo Man",
				Rated:           "R",
				Released:        "1984-03-02T00:00:00Z",
				RunTime:         92,
				Director:        "Alex Cox",
				Writer:          "Alex Cox",
				CreateUsername:  u.Email,
				CreateTimestamp: "",
				UpdateUsername:  u.Email,
				UpdateTimestamp: "",
			},
		}

		// initialize standardResponse
		gotBody := standardResponse{}

		// decode the response body into the standardResponse (gotBody)
		err = DecoderErr(json.NewDecoder(rr.Result().Body).Decode(&gotBody))
		defer rr.Result().Body.Close()

		// Assert that there is no error after decoding the response body
		c.Assert(err, qt.IsNil)

		// quicktest uses Google's cmp library for DeepEqual comparisons. It
		// has some great options included with it. Below is an example of
		// ignoring certain fields...
		ignoreFields := cmpopts.IgnoreFields(standardResponse{},
			"Data.ExternalID", "Data.CreateTimestamp", "Data.UpdateTimestamp")

		// Assert that the response body (gotBody) is as expected (wantBody).
		// The External ID needs to be unique as the database unique index
		// requires it. As a result, the ExternalID field is ignored as part
		// of the comparison. The Create/Update timestamps are ignored as
		// well, as they are always unique.
		// I could put another interface into the domain logic to solve
		// for the timestamps and may do so later, but it's probably not
		// necessary
		c.Assert(gotBody, qt.CmpEquals(ignoreFields), wantBody)
	})

	t.Run("mock DB", func(t *testing.T) {
		// initialize quickest checker
		c := qt.New(t)

		// initialize a zerolog Logger
		lgr := logger.NewLogger(os.Stdout, true)

		// initialize MockTransactor for the moviestore
		mockTransactor := moviestoretest.NewMockTransactor(t)

		// initialize MockSelector for the moviestore
		mockSelector := moviestoretest.NewMockSelector(t)

		// initialize mockAccessTokenConverter
		mockAccessTokenConverter := authtest.NewMockAccessTokenConverter(t)

		// initialize DefaultMovieHandlers
		dmh := DefaultMovieHandlers{
			RandomStringGenerator: randomtest.NewMockStringGenerator(t),
			AccessTokenConverter:  mockAccessTokenConverter,
			Authorizer:            authtest.NewMockAuthorizer(t),
			Transactor:            mockTransactor,
			Selector:              mockSelector,
		}

		// setup request body using anonymous struct
		requestBody := struct {
			Title    string `json:"title"`
			Rated    string `json:"rated"`
			Released string `json:"release_date"`
			RunTime  int    `json:"run_time"`
			Director string `json:"director"`
			Writer   string `json:"writer"`
		}{
			Title:    "Repo Man",
			Rated:    "R",
			Released: "1984-03-02T00:00:00Z",
			RunTime:  92,
			Director: "Alex Cox",
			Writer:   "Alex Cox",
		}

		// encode request body into buffer variable
		var buf bytes.Buffer
		err := json.NewEncoder(&buf).Encode(requestBody)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}

		// setup path
		path := pathPrefix + moviesV1PathRoot

		// form request using httptest
		req := httptest.NewRequest(http.MethodPost, path, &buf)

		// add test access token
		req.Header.Add("Authorization", auth.BearerTokenType+" abc123def1")

		// create middleware to extract the request ID from
		// the request context for testing comparison
		var requestID string
		requestIDMiddleware := func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rID, ok := hlog.IDFromRequest(r)
				if !ok {
					t.Fatal("Request ID not set to request context")
				}
				requestID = rID.String()

				h.ServeHTTP(w, r)
			})
		}

		// retrieve createMovieHandler HTTP handler
		createMovieHandler := ProvideCreateMovieHandler(dmh)

		// initialize ResponseRecorder to use with ServeHTTP as it
		// satisfies ResponseWriter interface and records the response
		// for testing
		rr := httptest.NewRecorder()

		// initialize alice Chain to chain middleware
		ac := alice.New()

		// setup full handler chain needed for request
		h := LoggerHandlerChain(lgr, ac).
			Append(AccessTokenHandler).
			Append(JSONContentTypeHandler).
			Append(requestIDMiddleware).
			Then(createMovieHandler)

		// call the handler ServeHTTP method to execute the request
		// and record the response
		h.ServeHTTP(rr, req)

		// Assert that Response Status Code equals 200 (StatusOK)
		c.Assert(rr.Code, qt.Equals, http.StatusOK)

		// createMovieResponse is the response struct for a Movie
		// the response struct is tucked inside the handler, so we
		// have to recreate it here
		type createMovieResponse struct {
			ExternalID      string `json:"external_id"`
			Title           string `json:"title"`
			Rated           string `json:"rated"`
			Released        string `json:"release_date"`
			RunTime         int    `json:"run_time"`
			Director        string `json:"director"`
			Writer          string `json:"writer"`
			CreateUsername  string `json:"create_username"`
			CreateTimestamp string `json:"create_timestamp"`
			UpdateUsername  string `json:"update_username"`
			UpdateTimestamp string `json:"update_timestamp"`
		}

		// standardResponse is the standard response struct used for
		// all response bodies, the Data field is actually an
		// interface{} in the real struct (handler.StandardResponse),
		// but it's easiest to decode to JSON using a proper struct
		// as below
		type standardResponse struct {
			Path      string              `json:"path"`
			RequestID string              `json:"request_id"`
			Data      createMovieResponse `json:"data"`
		}

		// retrieve the mock User that is used for testing
		u, _ := mockAccessTokenConverter.Convert(req.Context(), authtest.NewAccessToken(t))

		// setup the expected response data
		wantBody := standardResponse{
			Path:      path,
			RequestID: requestID,
			Data: createMovieResponse{
				ExternalID:      "superRandomString",
				Title:           "Repo Man",
				Rated:           "R",
				Released:        "1984-03-02T00:00:00Z",
				RunTime:         92,
				Director:        "Alex Cox",
				Writer:          "Alex Cox",
				CreateUsername:  u.Email,
				CreateTimestamp: time.Date(2008, 1, 8, 06, 54, 0, 0, time.UTC).String(),
				UpdateUsername:  u.Email,
				UpdateTimestamp: time.Date(2008, 1, 8, 06, 54, 0, 0, time.UTC).String(),
			},
		}

		// initialize standardResponse
		gotBody := standardResponse{}

		// decode the response body into the standardResponse (gotBody)
		err = DecoderErr(json.NewDecoder(rr.Result().Body).Decode(&gotBody))
		defer rr.Result().Body.Close()

		// Assert that there is no error after decoding the response body
		c.Assert(err, qt.IsNil)

		// quicktest uses Google's cmp library for DeepEqual comparisons. It
		// has some great options included with it. Below is an example of
		// ignoring certain fields...
		ignoreFields := cmpopts.IgnoreFields(standardResponse{},
			"Data.CreateTimestamp", "Data.UpdateTimestamp")

		// Assert that the response body (gotBody) is as expected (wantBody).
		// The Create/Update timestamps are ignored as they are always unique.
		// I could put another interface into the domain logic to solve
		// for this and may do so later.
		c.Assert(gotBody, qt.CmpEquals(ignoreFields), wantBody)
	})
}

func TestDefaultMovieHandlers_UpdateMovie(t *testing.T) {
	t.Run("typical", func(t *testing.T) {
		// set environment variable NO_DB to skip database
		// dependent tests
		if os.Getenv("NO_DB") == "true" {
			t.Skip("skipping db dependent test")
		}

		// initialize quickest checker
		c := qt.New(t)

		// initialize a zerolog Logger
		lgr := logger.NewLogger(os.Stdout, true)

		// initialize DefaultDatastore
		ds, cleanup := datastoretest.NewDefaultDatastore(t, lgr)

		// defer cleanup of the database until after the test is completed
		t.Cleanup(cleanup)

		// create a test movie in the database
		m, movieCleanup := moviestore.NewMovieDBHelper(t, context.Background(), ds)

		// defer cleanup of movie record until after the test is completed
		t.Cleanup(movieCleanup)

		// NewMovieDBHelper is

		// initialize the DefaultTransactor for the moviestore
		transactor := moviestore.NewDefaultTransactor(ds)

		// initialize the DefaultSelector for the moviestore
		selector := moviestore.NewDefaultSelector(ds)

		// initialize mockAccessTokenConverter
		mockAccessTokenConverter := authtest.NewMockAccessTokenConverter(t)

		// initialize DefaultStringGenerator
		randomStringGenerator := random.DefaultStringGenerator{}

		// initialize DefaultMovieHandlers
		dmh := DefaultMovieHandlers{
			RandomStringGenerator: randomStringGenerator,
			AccessTokenConverter:  mockAccessTokenConverter,
			Authorizer:            authtest.NewMockAuthorizer(t),
			Transactor:            transactor,
			Selector:              selector,
		}

		// setup request body using anonymous struct
		requestBody := struct {
			Title    string `json:"title"`
			Rated    string `json:"rated"`
			Released string `json:"release_date"`
			RunTime  int    `json:"run_time"`
			Director string `json:"director"`
			Writer   string `json:"writer"`
		}{
			Title:    "Repo Man",
			Rated:    "R",
			Released: "1984-03-02T00:00:00Z",
			RunTime:  92,
			Director: "Alex Cox",
			Writer:   "Alex Cox",
		}

		// encode request body into buffer variable
		var buf bytes.Buffer
		err := json.NewEncoder(&buf).Encode(requestBody)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}

		// setup path
		path := pathPrefix + moviesV1PathRoot + "/" + m.ExternalID

		// form request using httptest
		req := httptest.NewRequest(http.MethodPost, path, &buf)

		// add test access token
		req.Header.Add("Authorization", auth.BearerTokenType+" abc123def1")

		// create middleware to extract the request ID from
		// the request context for testing comparison
		var requestID string
		requestIDMiddleware := func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rID, ok := hlog.IDFromRequest(r)
				if !ok {
					t.Fatal("Request ID not set to request context")
				}
				requestID = rID.String()

				h.ServeHTTP(w, r)
			})
		}

		// retrieve createMovieHandler HTTP handler
		updateMovieHandler := ProvideUpdateMovieHandler(dmh)

		// initialize ResponseRecorder to use with ServeHTTP as it
		// satisfies ResponseWriter interface and records the response
		// for testing
		rr := httptest.NewRecorder()

		// initialize alice Chain to chain middleware
		ac := alice.New()

		// setup full handler chain needed for request
		h := LoggerHandlerChain(lgr, ac).
			Append(AccessTokenHandler).
			Append(JSONContentTypeHandler).
			Append(requestIDMiddleware).
			Then(updateMovieHandler)

		// handler needs path variable, so we need to use mux router
		router := mux.NewRouter()
		// setup the expected path and route variable
		router.Handle(pathPrefix+moviesV1PathRoot+"/{extlID}", h)
		// call the router ServeHTTP method to execute the request
		// and record the response
		router.ServeHTTP(rr, req)

		// Assert that Response Status Code equals 200 (StatusOK)
		c.Assert(rr.Code, qt.Equals, http.StatusOK)

		// updateMovieResponse is the response struct for updating a
		// Movie. The response struct is tucked inside the handler,
		// so we have to recreate it here
		type updateMovieResponse struct {
			ExternalID      string `json:"external_id"`
			Title           string `json:"title"`
			Rated           string `json:"rated"`
			Released        string `json:"release_date"`
			RunTime         int    `json:"run_time"`
			Director        string `json:"director"`
			Writer          string `json:"writer"`
			CreateUsername  string `json:"create_username"`
			CreateTimestamp string `json:"create_timestamp"`
			UpdateUsername  string `json:"update_username"`
			UpdateTimestamp string `json:"update_timestamp"`
		}

		// standardResponse is the standard response struct used for
		// all response bodies, the Data field is actually an
		// interface{} in the real struct (handler.StandardResponse),
		// but it's easiest to decode to JSON using a proper struct
		// as below
		type standardResponse struct {
			Path      string              `json:"path"`
			RequestID string              `json:"request_id"`
			Data      updateMovieResponse `json:"data"`
		}

		// retrieve the mock User that is used for testing
		u, _ := mockAccessTokenConverter.Convert(req.Context(), authtest.NewAccessToken(t))

		// setup the expected response data
		wantBody := standardResponse{
			Path:      path,
			RequestID: requestID,
			Data: updateMovieResponse{
				//ExternalID:      "superRandomString",
				Title:          "Repo Man",
				Rated:          "R",
				Released:       "1984-03-02T00:00:00Z",
				RunTime:        92,
				Director:       "Alex Cox",
				Writer:         "Alex Cox",
				CreateUsername: u.Email,
				//CreateTimestamp: "",
				UpdateUsername: u.Email,
				//UpdateTimestamp: "",
			},
		}

		// initialize standardResponse
		gotBody := standardResponse{}

		// decode the response body into the standardResponse (gotBody)
		err = DecoderErr(json.NewDecoder(rr.Result().Body).Decode(&gotBody))
		defer rr.Result().Body.Close()

		// Assert that there is no error after decoding the response body
		c.Assert(err, qt.IsNil)

		// quicktest uses Google's cmp library for DeepEqual comparisons. It
		// has some great options included with it. Below is an example of
		// ignoring certain fields...
		ignoreFields := cmpopts.IgnoreFields(standardResponse{},
			"Data.ExternalID", "Data.CreateTimestamp", "Data.UpdateTimestamp")

		// Assert that the response body (gotBody) is as expected (wantBody).
		// The External ID needs to be unique as the database unique index
		// requires it. As a result, the ExternalID field is ignored as part
		// of the comparison. The Create/Update timestamps are ignored as
		// well, as they are always unique.
		// I could put another interface into the domain logic to solve
		// for the timestamps and may do so later, but it's probably not
		// necessary
		c.Assert(gotBody, qt.CmpEquals(ignoreFields), wantBody)
	})
}
