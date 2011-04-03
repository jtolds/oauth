package oauth

import (
	"crypto/hmac"
	"encoding/base64"
	"fmt"
	"http"
	"io/ioutil"
	"os"
	"rand"
	"sort"
	"strconv"
	"time"
)

const (
	OAUTH_VERSION    = "1.0"
	SIGNATURE_METHOD = "HMAC-SHA1"

	CALLBACK_PARAM         = "oauth_callback"
	CONSUMER_KEY_PARAM     = "oauth_consumer_key"
	NONCE_PARAM            = "oauth_nonce"
	SIGNATURE_METHOD_PARAM = "oauth_signature_method"
	SIGNATURE_PARAM        = "oauth_signature"
	TIMESTAMP_PARAM        = "oauth_timestamp"
	TOKEN_PARAM            = "oauth_token"
	TOKEN_SECRET_PARAM     = "oauth_token_secret"
	VERIFIER_PARAM         = "oauth_verifier"
	VERSION_PARAM          = "oauth_version"
)

type Consumer struct {
	// Get these from the OAuth Service Provider
	ConsumerKey    string
	ConsumerSecret string

	RequestTokenUrl   string
	AuthorizeTokenUrl string
	AccessTokenUrl    string

	CallbackUrl      string
	AdditionalParams map[string]string

	HttpClient     HttpClient
	Clock          Clock
	NonceGenerator NonceGenerator
}

type UnauthorizedToken struct {
	Token       string
	TokenSecret string
}

type AuthorizedToken struct {
	Token       string
	TokenSecret string
}

type request struct {
	method      string
	url         string
	oauthParams *OrderedParams
	userParams  map[string]string
}

type HttpClient interface {
	Do(req *http.Request) (resp *http.Response, err os.Error)
}

type Clock interface {
	Seconds() int64
}

type NonceGenerator interface {
	Int63() int64
}

type DefaultClock struct{}

func (*DefaultClock) Seconds() int64 {
	return time.Seconds()
}

func newGetRequest(url string, oauthParams *OrderedParams) *request {
	return &request{
		method:      "GET",
		url:         url,
		oauthParams: oauthParams,
	}
}

func (c *Consumer) GetRequestToken() (*UnauthorizedToken, os.Error) {
	params := c.baseParams(c.ConsumerKey, c.AdditionalParams)
	params.Add(CALLBACK_PARAM, c.CallbackUrl)

	req := newGetRequest(c.RequestTokenUrl, params)
	c.signRequest(req, c.makeKey("")) // We don't have a token secret for the key yet

	resp, err := c.getBody(c.RequestTokenUrl, params)
	if err != nil {
		return nil, err
	}

	token, secret, err := parseTokenAndSecret(*resp)
	if err != nil {
		return nil, err
	}
	return &UnauthorizedToken{
		Token:       *token,
		TokenSecret: *secret,
	},
		nil
}

func (c *Consumer) signRequest(req *request, key string) *request {
	base_string := c.requestString(req.method, req.url, req.oauthParams)
	req.oauthParams.Add(SIGNATURE_PARAM, sign(base_string, key))
	return req
}

func (c *Consumer) TokenAuthorizationUrl(token *UnauthorizedToken) string {
	return c.AuthorizeTokenUrl + "?oauth_token=" + token.Token
}

func (c *Consumer) AuthorizeToken(unauthToken *UnauthorizedToken, verificationCode string) (*AuthorizedToken, os.Error) {
	params := c.baseParams(c.ConsumerKey, c.AdditionalParams)

	params.Add(VERIFIER_PARAM, verificationCode)
	params.Add(TOKEN_PARAM, unauthToken.Token)

	req := newGetRequest(c.AccessTokenUrl, params)
	c.signRequest(req, c.makeKey(unauthToken.TokenSecret))

	resp, err := c.getBody(c.AccessTokenUrl, params)

	token, secret, err := parseTokenAndSecret(*resp)
	if err != nil {
		return nil, err
	}
	return &AuthorizedToken{
		Token:       *token,
		TokenSecret: *secret,
	},
		nil
}

func (c *Consumer) Get(url string, userParams map[string]string, token *AuthorizedToken) (*http.Response, os.Error) {
	allParams := c.baseParams(c.ConsumerKey, c.AdditionalParams)
	authParams := allParams.Clone()

	queryParams := ""
	separator := "?"
	if userParams != nil {
		for key, value := range userParams {
			allParams.Add(key, value)
			queryParams += separator + escape(key) + "=" + escape(value)
			separator = "&"
		}
	}

	allParams.Add(TOKEN_PARAM, token.Token)
	authParams.Add(TOKEN_PARAM, token.Token)

	key := c.makeKey(token.TokenSecret)

	base_string := c.requestString("GET", url, allParams)
	authParams.Add(SIGNATURE_PARAM, sign(base_string, key))

	return c.get(url+queryParams, authParams)
}

func (c *Consumer) makeKey(tokenSecret string) string {
	return escape(c.ConsumerSecret) + "&" + escape(tokenSecret)
}

func parseTokenAndSecret(data string) (*string, *string, os.Error) {
	parts, err := http.ParseQuery(data)
	if err != nil {
		return nil, nil, err
	}

	if len(parts[TOKEN_PARAM]) < 1 {
		return nil, nil, os.NewError("Missing " + TOKEN_PARAM + " in response.")
	}
	if len(parts[TOKEN_SECRET_PARAM]) < 1 {
		return nil, nil, os.NewError("Missing " + TOKEN_SECRET_PARAM + " in response.")
	}

	return &parts[TOKEN_PARAM][0], &parts[TOKEN_SECRET_PARAM][0], nil
}

func (c *Consumer) init() {
	if c.Clock == nil {
		c.Clock = &DefaultClock{}
	}
	if c.HttpClient == nil {
		c.HttpClient = &http.Client{}
	}
	if c.NonceGenerator == nil {
		c.NonceGenerator = rand.New(rand.NewSource(c.Clock.Seconds()))
	}
}

func (c *Consumer) baseParams(consumerKey string, additionalParams map[string]string) *OrderedParams {
	c.init()
	params := NewOrderedParams()
	params.Add(VERSION_PARAM, OAUTH_VERSION)
	params.Add(SIGNATURE_METHOD_PARAM, SIGNATURE_METHOD)
	params.Add(TIMESTAMP_PARAM, strconv.Itoa64(c.Clock.Seconds()))
	params.Add(NONCE_PARAM, strconv.Itoa64(c.NonceGenerator.Int63()))
	params.Add(CONSUMER_KEY_PARAM, consumerKey)
	for key, value := range additionalParams {
		params.Add(key, value)
	}
	return params
}

func sign(message string, key string) string {
	fmt.Println("Signing:" + message)
	fmt.Println("Key:" + key)
	hashfun := hmac.NewSHA1([]byte(key))
	hashfun.Write([]byte(message))
	rawsignature := hashfun.Sum()
	base64signature := make([]byte, base64.StdEncoding.EncodedLen(len(rawsignature)))
	base64.StdEncoding.Encode(base64signature, rawsignature)
	return string(base64signature)
}

func escape(input string) string {
	return http.URLEscape(input)
}

func (c *Consumer) requestString(method string, url string, params *OrderedParams) string {
	result := method + "&" + escape(url)
	for pos, key := range params.Keys() {
		if pos == 0 {
			result += "&"
		} else {
			result += escape("&")
		}
		result += escape(fmt.Sprintf("%s=%s", key, params.Get(key)))
	}
	return result
}

func (c *Consumer) getBody(url string, oauthParams *OrderedParams) (*string, os.Error) {
	resp, err := c.get(url, oauthParams)
	if err != nil {
		return nil, err
	}
	fmt.Println("About to readbody")
	bytes, err := ioutil.ReadAll(resp.Body)
	fmt.Println("Done readbody")
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	str := string(bytes)
	fmt.Println("BODY RESPONSE: " + str)
	return &str, nil
}

func (c *Consumer) get(url string, oauthParams *OrderedParams) (*http.Response, os.Error) {
	fmt.Println("GET url: " + url)

	var req http.Request
	req.Method = "GET"
	req.Header = http.Header{}
	parsedurl, err := http.ParseURL(url)
	if err != nil {
		return nil, err
	}
	req.URL = parsedurl

	authhdr := "OAuth "
	for pos, key := range oauthParams.Keys() {
		if pos > 0 {
			authhdr += ",\n    "
		}
		authhdr += key + "=\"" + oauthParams.Get(key) + "\""
	}
	fmt.Println("AUTH-HDR: " + authhdr)
	req.Header.Add("Authorization", authhdr)

	return c.HttpClient.Do(&req)
}

//
// ORDERED PARAMS
//

type OrderedParams struct {
	allParams   map[string]string
	keyOrdering []string
}

func NewOrderedParams() *OrderedParams {
	return &OrderedParams{
		allParams:   make(map[string]string),
		keyOrdering: make([]string, 0),
	}
}

func (o *OrderedParams) Get(key string) string {
	return o.allParams[key]
}

func (o *OrderedParams) Keys() []string {
	sort.Sort(o)
	return o.keyOrdering
}

func (o *OrderedParams) Add(key, value string) {
	o.add(key, http.URLEscape(value))
}

func (o *OrderedParams) add(key, value string) {
	o.allParams[key] = value
	o.keyOrdering = append(o.keyOrdering, key)
}

func (o *OrderedParams) Len() int {
	return len(o.keyOrdering)
}

func (o *OrderedParams) Less(i int, j int) bool {
	return o.keyOrdering[i] < o.keyOrdering[j]
}

func (o *OrderedParams) Swap(i int, j int) {
	o.keyOrdering[i], o.keyOrdering[j] = o.keyOrdering[j], o.keyOrdering[i]
}

func (o *OrderedParams) Clone() *OrderedParams {
	clone := NewOrderedParams()
	for _, key := range o.Keys() {
		clone.add(key, o.Get(key))
	}
	return clone
}
