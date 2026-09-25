// Package paypal은 PayPal Orders v2/Payments v2 REST adapter다. Sandbox와 LIVE는
// base URL·credential·webhook namespace로 완전히 분리되고, 모든 write는 호출자가
// 미리 저장한 PayPal-Request-Id를 사용한다. redirect/webhook은 wake-up 신호일 뿐
// 성공 권위가 아니므로 이 client는 GET 재조회를 함께 제공한다.
package paypal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

const (
	SandboxBaseURL = "https://api-m.sandbox.paypal.com"
	LiveBaseURL    = "https://api-m.paypal.com"
)

var (
	// ErrOutcomeUnknown은 write 전송 뒤 결과를 확정할 수 없는 실패다(timeout, 응답
	// 유실). 호출자는 새 effect를 만들지 않고 같은 key 재시도 또는 GET으로 대사한다.
	ErrOutcomeUnknown = errors.New("PAYPAL_OUTCOME_UNKNOWN")
	// ErrNonconformingResponse는 provider 응답이 Vitlane이 보낸 Order/Authorization/
	// Capture 경계와 맞지 않아 채택할 수 없음을 뜻한다. provider success status라도
	// 이 오류이면 merchant effect를 시작하지 않고 fresh GET으로 대사한다.
	ErrNonconformingResponse = errors.New("PAYPAL_RESPONSE_NONCONFORMING")
	ErrInvalidInput          = errors.New("PAYPAL_INPUT_INVALID")
)

// APIError는 provider가 명시적으로 거절한 응답이다.
type APIError struct {
	StatusCode int
	Name       string
	IssueCode  string
	DebugID    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("paypal api error status=%d name=%s issue=%s debug=%s",
		e.StatusCode, e.Name, e.IssueCode, e.DebugID)
}

func (e *APIError) Unwrap() error {
	if e.IssueCode == "PREVIOUS_REQUEST_IN_PROGRESS" ||
		e.Name == "PREVIOUS_REQUEST_IN_PROGRESS" {
		return ErrOutcomeUnknown
	}
	return nil
}

type Config struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
	// HTTPClient는 shared httpclient가 만든 managed client다. 이 패키지는 직접
	// http.Client를 만들지 않는다(runtime boundary guard).
	HTTPClient *http.Client
}

type Client struct {
	config Config
	http   *http.Client

	tokenMu      sync.Mutex
	token        string
	tokenExpires time.Time
}

func NewClient(config Config) (*Client, error) {
	if strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.ClientID) == "" ||
		strings.TrimSpace(config.ClientSecret) == "" || config.HTTPClient == nil {
		return nil, errors.New("paypal client requires base URL, credentials and a managed HTTP client")
	}
	return &Client{config: config, http: config.HTTPClient}, nil
}

// accessToken은 client-credentials 토큰을 exp-5m까지 캐시한다.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token != "" && time.Now().Add(5*time.Minute).Before(c.tokenExpires) {
		return c.token, nil
	}
	body := strings.NewReader("grant_type=client_credentials")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.config.BaseURL+"/v1/oauth2/token", body)
	if err != nil {
		return "", err
	}
	basic := base64.StdEncoding.EncodeToString(
		[]byte(c.config.ClientID + ":" + c.config.ClientSecret))
	request.Header.Set("Authorization", "Basic "+basic)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := sharedhttpclient.Do(ctx, c.http, request, sharedhttpclient.ReadOnly)
	if err != nil {
		return "", fmt.Errorf("paypal token request: %w", err)
	}
	defer response.Body.Close()
	payload, err := sharedhttpclient.ReadBody(response.Body, 1<<20)
	if err != nil {
		return "", fmt.Errorf("paypal token read: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return "", &APIError{StatusCode: response.StatusCode, Name: "TOKEN_REQUEST_FAILED"}
	}
	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil || parsed.AccessToken == "" {
		return "", errors.New("paypal token response invalid")
	}
	c.token = parsed.AccessToken
	c.tokenExpires = time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	return c.token, nil
}

type Amount struct {
	CurrencyCode string `json:"currency_code"`
	Value        string `json:"value"`
}

// MinorToValue는 센트 정수를 PayPal 십진 문자열로 변환한다. float 금지.
func MinorToValue(minor int64) string {
	return strconv.FormatInt(minor/100, 10) + "." + fmt.Sprintf("%02d", minor%100)
}

// ValueToMinor는 PayPal 십진 문자열을 센트 정수로 변환한다.
func ValueToMinor(value string) (int64, error) {
	parts := strings.SplitN(strings.TrimSpace(value), ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole < 0 {
		return 0, fmt.Errorf("paypal amount invalid: %q", value)
	}
	var cents int64
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) > 2 {
			return 0, fmt.Errorf("paypal amount precision invalid: %q", value)
		}
		for len(fraction) < 2 {
			fraction += "0"
		}
		cents, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil || cents < 0 {
			return 0, fmt.Errorf("paypal amount invalid: %q", value)
		}
	}
	return whole*100 + cents, nil
}

type CreateOrderInput struct {
	RequestID   string
	ReferenceID string
	CustomID    string
	AmountMinor int64
	ReturnURL   string
	CancelURL   string
}

type Order struct {
	ID                         string
	Intent                     string
	Status                     string
	PayerActionURL             string
	AmountMinor                int64
	Currency                   string
	PayeeMerchant              string
	CaptureID                  string
	CaptureStatus              string
	CaptureMinor               int64
	CaptureTime                string
	CaptureEconomicsReconciled bool
	ProcessorFeeMinor          int64
	NetReceivableMinor         int64
	Authorizations             []Authorization
	Captures                   []Capture
}

// SellerReceivableBreakdown은 PayPal이 각 Capture에 대해 보고한 실제 gross,
// processor fee와 net이다. 호출자는 이를 forecast fee로 다시 계산하지 않는다.
type SellerReceivableBreakdown struct {
	GrossAmount Amount `json:"gross_amount"`
	PayPalFee   Amount `json:"paypal_fee"`
	NetAmount   Amount `json:"net_amount"`
}

type captureResponse struct {
	ID                        string                     `json:"id"`
	Status                    string                     `json:"status"`
	Amount                    Amount                     `json:"amount"`
	InvoiceID                 string                     `json:"invoice_id"`
	FinalCapture              bool                       `json:"final_capture"`
	CreateTime                string                     `json:"create_time"`
	UpdateTime                string                     `json:"update_time"`
	SellerReceivableBreakdown *SellerReceivableBreakdown `json:"seller_receivable_breakdown"`
}

type authorizationResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Amount    Amount `json:"amount"`
	InvoiceID string `json:"invoice_id"`
	CustomID  string `json:"custom_id"`
	Payee     struct {
		MerchantID string `json:"merchant_id"`
	} `json:"payee"`
	SupplementaryData struct {
		RelatedIDs struct {
			OrderID string `json:"order_id"`
		} `json:"related_ids"`
	} `json:"supplementary_data"`
	Links []struct {
		Href string `json:"href"`
		Rel  string `json:"rel"`
	} `json:"links"`
	ExpirationTime string `json:"expiration_time"`
	CreateTime     string `json:"create_time"`
	UpdateTime     string `json:"update_time"`
}

type orderResponse struct {
	ID            string `json:"id"`
	Intent        string `json:"intent"`
	Status        string `json:"status"`
	PurchaseUnits []struct {
		Amount Amount `json:"amount"`
		Payee  struct {
			MerchantID string `json:"merchant_id"`
		} `json:"payee"`
		Payments struct {
			Authorizations []authorizationResponse `json:"authorizations"`
			Captures       []captureResponse       `json:"captures"`
		} `json:"payments"`
	} `json:"purchase_units"`
	Links []struct {
		Href string `json:"href"`
		Rel  string `json:"rel"`
	} `json:"links"`
}

func (r orderResponse) toOrder() (Order, error) {
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.Status) == "" {
		return Order{}, nonconforming("order identity or status is missing")
	}
	if len(r.PurchaseUnits) != 1 {
		return Order{}, nonconforming("AUTHORIZE order must contain exactly one purchase unit")
	}
	order := Order{
		ID: strings.TrimSpace(r.ID), Intent: strings.TrimSpace(r.Intent),
		Status: strings.TrimSpace(r.Status),
	}
	for _, link := range r.Links {
		if link.Rel == "payer-action" || link.Rel == "approve" {
			order.PayerActionURL = strings.TrimSpace(link.Href)
		}
	}
	if order.Status == "CREATED" || order.Status == "PAYER_ACTION_REQUIRED" {
		payerAction, err := url.Parse(order.PayerActionURL)
		if err != nil || payerAction.Scheme != "https" || payerAction.Host == "" || payerAction.User != nil {
			return Order{}, nonconforming("pre-approval order has no valid HTTPS payer-action URL")
		}
	}
	unit := r.PurchaseUnits[0]
	order.Currency = strings.TrimSpace(unit.Amount.CurrencyCode)
	order.PayeeMerchant = strings.TrimSpace(unit.Payee.MerchantID)
	if unit.Amount.Value == "" || order.Currency == "" {
		return Order{}, nonconforming("purchase unit amount is missing")
	}
	minor, err := ValueToMinor(unit.Amount.Value)
	if err != nil {
		return Order{}, nonconforming("purchase unit amount is invalid: %v", err)
	}
	order.AmountMinor = minor
	for _, raw := range unit.Payments.Authorizations {
		authorization, parseErr := raw.toAuthorization()
		if parseErr != nil {
			return Order{}, parseErr
		}
		order.Authorizations = append(order.Authorizations, authorization)
	}
	for _, raw := range unit.Payments.Captures {
		capture, parseErr := raw.toCapture()
		if parseErr != nil {
			return Order{}, parseErr
		}
		if capture.Currency != order.Currency {
			return Order{}, nonconforming("capture currency does not match purchase unit")
		}
		order.Captures = append(order.Captures, capture)
	}
	if len(order.Captures) > 0 {
		capture := order.Captures[0]
		order.CaptureID = capture.ID
		order.CaptureStatus = capture.Status
		order.CaptureTime = capture.CreateTime
		order.CaptureMinor = capture.AmountMinor
		order.ProcessorFeeMinor = capture.ProcessorFeeMinor
		order.NetReceivableMinor = capture.NetReceivableMinor
		order.CaptureEconomicsReconciled = capture.EconomicsReconciled
	}
	return order, nil
}

func (r authorizationResponse) toAuthorization() (Authorization, error) {
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.Status) == "" ||
		strings.TrimSpace(r.Amount.CurrencyCode) == "" || strings.TrimSpace(r.Amount.Value) == "" {
		return Authorization{}, nonconforming("authorization identity, status or amount is missing")
	}
	minor, err := ValueToMinor(r.Amount.Value)
	if err != nil {
		return Authorization{}, nonconforming("authorization amount is invalid: %v", err)
	}
	authorization := Authorization{
		ID: strings.TrimSpace(r.ID), Status: strings.TrimSpace(r.Status),
		AmountMinor: minor, Currency: strings.TrimSpace(r.Amount.CurrencyCode),
		InvoiceID: strings.TrimSpace(r.InvoiceID), CustomID: strings.TrimSpace(r.CustomID),
		ParentOrderID:  strings.TrimSpace(r.SupplementaryData.RelatedIDs.OrderID),
		PayeeMerchant:  strings.TrimSpace(r.Payee.MerchantID),
		ExpirationTime: strings.TrimSpace(r.ExpirationTime),
		CreateTime:     strings.TrimSpace(r.CreateTime), UpdateTime: strings.TrimSpace(r.UpdateTime),
	}
	for _, link := range r.Links {
		if link.Rel != "up" {
			continue
		}
		orderID, linkErr := linkedPayPalCheckoutOrderID(link.Href)
		if linkErr != nil {
			return authorization, nonconforming("authorization parent order link is invalid")
		}
		if authorization.ParentOrderID != "" && authorization.ParentOrderID != orderID {
			return authorization, nonconforming("authorization parent order identities conflict")
		}
		authorization.ParentOrderID = orderID
	}
	return authorization, nil
}

func (r captureResponse) toCapture() (Capture, error) {
	capture := Capture{
		ID: strings.TrimSpace(r.ID), Status: strings.TrimSpace(r.Status),
		Currency:  strings.TrimSpace(r.Amount.CurrencyCode),
		InvoiceID: strings.TrimSpace(r.InvoiceID), FinalCapture: r.FinalCapture,
		CreateTime: strings.TrimSpace(r.CreateTime), UpdateTime: strings.TrimSpace(r.UpdateTime),
		SellerReceivableBreakdown: r.SellerReceivableBreakdown,
	}
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.Status) == "" ||
		strings.TrimSpace(r.Amount.CurrencyCode) == "" || strings.TrimSpace(r.Amount.Value) == "" {
		return capture, nonconforming("capture identity, status or amount is missing")
	}
	minor, err := ValueToMinor(r.Amount.Value)
	if err != nil {
		return capture, nonconforming("capture amount is invalid: %v", err)
	}
	feeMinor, netMinor, reconciled := captureEconomics(r.Amount, r.SellerReceivableBreakdown)
	capture.AmountMinor = minor
	capture.EconomicsReconciled = reconciled
	capture.ProcessorFeeMinor = feeMinor
	capture.NetReceivableMinor = netMinor
	return capture, nil
}

func nonconforming(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrNonconformingResponse, fmt.Sprintf(format, args...))
}

func invalidInput(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}

// captureEconomics는 PayPal이 보고한 seller receivable breakdown이 완전하고
// gross-fee=net일 때만 채택한다. 누락·불일치는 추정하지 않고 미대사로 남긴다.
func captureEconomics(captureAmount Amount, breakdown *SellerReceivableBreakdown) (int64, int64, bool) {
	if breakdown == nil || breakdown.GrossAmount.Value == "" ||
		breakdown.PayPalFee.Value == "" || breakdown.NetAmount.Value == "" {
		return 0, 0, false
	}
	currency := captureAmount.CurrencyCode
	if currency == "" || breakdown.GrossAmount.CurrencyCode != currency ||
		breakdown.PayPalFee.CurrencyCode != currency || breakdown.NetAmount.CurrencyCode != currency {
		return 0, 0, false
	}
	captureMinor, captureErr := ValueToMinor(captureAmount.Value)
	grossMinor, grossErr := ValueToMinor(breakdown.GrossAmount.Value)
	feeMinor, feeErr := ValueToMinor(breakdown.PayPalFee.Value)
	netMinor, netErr := ValueToMinor(breakdown.NetAmount.Value)
	if captureErr != nil || grossErr != nil || feeErr != nil || netErr != nil ||
		captureMinor != grossMinor || grossMinor-feeMinor != netMinor {
		return 0, 0, false
	}
	return feeMinor, netMinor, true
}

// CreateOrder는 전체 AgencyOrder gross를 하나의 purchase_unit으로 묶은
// intent=AUTHORIZE 주문을 만든다. 내부 MerchantOrder를 PayPal purchase_unit으로
// 분리하지 않는다. 같은 RequestID 재시도는 provider에서 idempotent다.
func (c *Client) CreateOrder(ctx context.Context, input CreateOrderInput) (Order, error) {
	if strings.TrimSpace(input.RequestID) == "" || strings.TrimSpace(input.ReferenceID) == "" ||
		strings.TrimSpace(input.CustomID) == "" || input.AmountMinor <= 0 ||
		strings.TrimSpace(input.ReturnURL) == "" || strings.TrimSpace(input.CancelURL) == "" {
		return Order{}, invalidInput("create order requires stable ids, positive amount and return URLs")
	}
	payload := map[string]any{
		"intent": "AUTHORIZE",
		"purchase_units": []map[string]any{{
			"reference_id": input.ReferenceID,
			"custom_id":    input.CustomID,
			"amount": Amount{
				CurrencyCode: "USD", Value: MinorToValue(input.AmountMinor),
			},
		}},
		"payment_source": map[string]any{
			"paypal": map[string]any{
				"experience_context": map[string]any{
					"user_action":         "CONTINUE",
					"shipping_preference": "NO_SHIPPING",
					"return_url":          input.ReturnURL,
					"cancel_url":          input.CancelURL,
				},
			},
		},
	}
	var parsed orderResponse
	err := c.call(ctx, http.MethodPost, "/v2/checkout/orders", input.RequestID, payload, &parsed)
	if err != nil {
		return Order{}, err
	}
	order, err := parsed.toOrder()
	if err != nil {
		return Order{}, err
	}
	if order.AmountMinor != input.AmountMinor || order.Currency != "USD" {
		return Order{}, nonconforming("created order amount does not match request")
	}
	if order.Intent != "AUTHORIZE" {
		return Order{}, nonconforming("created order intent is not AUTHORIZE")
	}
	return order, nil
}

func (c *Client) GetOrder(ctx context.Context, orderID string) (Order, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return Order{}, invalidInput("order id is required")
	}
	var parsed orderResponse
	err := c.call(ctx, http.MethodGet,
		"/v2/checkout/orders/"+orderID, "", nil, &parsed)
	if err != nil {
		return Order{}, err
	}
	order, err := parsed.toOrder()
	if err != nil {
		return Order{}, err
	}
	if order.ID != orderID {
		return Order{}, nonconforming("order id does not match request")
	}
	return order, nil
}

// GetAuthorizedOrder는 fresh Order GET에 authorization resource가 존재하고 PayPal
// purchase_unit의 금액·통화와 정확히 일치하는지까지 검증한다.
func (c *Client) GetAuthorizedOrder(ctx context.Context, orderID string) (Order, error) {
	order, err := c.GetOrder(ctx, orderID)
	if err != nil {
		return Order{}, err
	}
	if err := validateAuthorizedOrder(order); err != nil {
		return Order{}, err
	}
	return order, nil
}

// AuthorizeOrder는 payer approval 뒤 Order 전체 gross를 한 번 authorize한다.
// PayPal Order status=COMPLETED는 Capture 완료를 뜻하지 않으므로 반환된
// authorization resource를 별도로 검증한다.
func (c *Client) AuthorizeOrder(
	ctx context.Context,
	orderID, requestID string,
) (Order, error) {
	orderID = strings.TrimSpace(orderID)
	requestID = strings.TrimSpace(requestID)
	if orderID == "" || requestID == "" {
		return Order{}, invalidInput("authorize order requires order id and request id")
	}
	var parsed orderResponse
	if err := c.call(ctx, http.MethodPost,
		"/v2/checkout/orders/"+orderID+"/authorize", requestID,
		map[string]any{}, &parsed); err != nil {
		return Order{}, err
	}
	order, err := parsed.toOrder()
	if err != nil {
		return Order{}, err
	}
	if order.ID != orderID {
		return Order{}, nonconforming("authorized order id does not match request")
	}
	if err := validateAuthorizedOrder(order); err != nil {
		return Order{}, err
	}
	return order, nil
}

func validateAuthorizedOrder(order Order) error {
	if order.Intent != "AUTHORIZE" {
		return nonconforming("authorized order intent is not AUTHORIZE")
	}
	if order.PayeeMerchant == "" {
		return nonconforming("authorized order payee merchant is missing")
	}
	if order.AmountMinor <= 0 || order.Currency != "USD" {
		return nonconforming("authorized order amount is missing")
	}
	if len(order.Authorizations) == 0 {
		return nonconforming("authorized order has no authorization resource")
	}
	matchingAuthorization := false
	for _, authorization := range order.Authorizations {
		if authorization.AmountMinor == order.AmountMinor && authorization.Currency == order.Currency {
			matchingAuthorization = true
		}
	}
	if !matchingAuthorization {
		return nonconforming("authorization does not match order amount")
	}
	return nil
}

type Authorization struct {
	ID             string
	Status         string
	AmountMinor    int64
	Currency       string
	InvoiceID      string
	CustomID       string
	ParentOrderID  string
	PayeeMerchant  string
	ExpirationTime string
	CreateTime     string
	UpdateTime     string
}

type Capture struct {
	ID                        string
	Status                    string
	AmountMinor               int64
	Currency                  string
	InvoiceID                 string
	FinalCapture              bool
	CreateTime                string
	UpdateTime                string
	EconomicsReconciled       bool
	ProcessorFeeMinor         int64
	NetReceivableMinor        int64
	SellerReceivableBreakdown *SellerReceivableBreakdown
}

func (c *Client) GetCapture(ctx context.Context, captureID string) (Capture, error) {
	captureID = strings.TrimSpace(captureID)
	if captureID == "" {
		return Capture{}, invalidInput("capture id is required")
	}
	var parsed captureResponse
	err := c.call(ctx, http.MethodGet,
		"/v2/payments/captures/"+captureID, "", nil, &parsed)
	if err != nil {
		return Capture{}, err
	}
	capture, err := parsed.toCapture()
	if err != nil {
		return Capture{}, err
	}
	if capture.ID != captureID {
		return Capture{}, nonconforming("capture id does not match request")
	}
	return capture, nil
}

func (c *Client) GetAuthorization(
	ctx context.Context,
	authorizationID string,
) (Authorization, error) {
	authorizationID = strings.TrimSpace(authorizationID)
	if authorizationID == "" {
		return Authorization{}, invalidInput("authorization id is required")
	}
	var parsed authorizationResponse
	if err := c.call(ctx, http.MethodGet,
		"/v2/payments/authorizations/"+authorizationID, "", nil, &parsed); err != nil {
		return Authorization{}, err
	}
	authorization, err := parsed.toAuthorization()
	if err != nil {
		return Authorization{}, err
	}
	if authorization.ID != authorizationID {
		return Authorization{}, nonconforming("authorization id does not match request")
	}
	return authorization, nil
}

type CaptureAuthorizationInput struct {
	AuthorizationID string
	RequestID       string
	InvoiceID       string
	AmountMinor     int64
	Currency        string
	FinalCapture    bool
}

type ReauthorizeAuthorizationInput struct {
	AuthorizationID string
	RequestID       string
	AmountMinor     int64
	Currency        string
	OrderID         string
	PayeeMerchant   string
}

// CaptureAuthorization은 한 Authorization에서 정확한 MO gross만 수납한다.
// final_capture는 호출자가 server-side active MO 집합을 잠근 뒤 결정한다.
func (c *Client) CaptureAuthorization(
	ctx context.Context,
	input CaptureAuthorizationInput,
) (Capture, error) {
	input.AuthorizationID = strings.TrimSpace(input.AuthorizationID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.InvoiceID = strings.TrimSpace(input.InvoiceID)
	input.Currency = strings.TrimSpace(input.Currency)
	if input.AuthorizationID == "" || input.RequestID == "" || input.InvoiceID == "" ||
		input.AmountMinor <= 0 || input.Currency != "USD" {
		return Capture{}, invalidInput("capture authorization requires ids, currency and positive amount")
	}
	payload := map[string]any{
		"amount":        Amount{CurrencyCode: input.Currency, Value: MinorToValue(input.AmountMinor)},
		"invoice_id":    input.InvoiceID,
		"final_capture": input.FinalCapture,
	}
	var parsed captureResponse
	if err := c.call(ctx, http.MethodPost,
		"/v2/payments/authorizations/"+input.AuthorizationID+"/capture",
		input.RequestID, payload, &parsed); err != nil {
		return Capture{}, err
	}
	capture, err := parsed.toCapture()
	if err != nil {
		return capture, err
	}
	if capture.AmountMinor != input.AmountMinor || capture.Currency != input.Currency ||
		capture.InvoiceID != input.InvoiceID || capture.FinalCapture != input.FinalCapture {
		return capture, nonconforming("capture response does not match MO capture request")
	}
	return capture, nil
}

// VoidAuthorization은 아직 capture되지 않은 전체 open amount를 취소한다.
// PayPal은 partial void를 지원하지 않으므로 MO 하나의 비례 hold 해제에는 쓰지 않는다.
func (c *Client) VoidAuthorization(
	ctx context.Context,
	authorizationID, requestID string,
) error {
	authorizationID = strings.TrimSpace(authorizationID)
	requestID = strings.TrimSpace(requestID)
	if authorizationID == "" || requestID == "" {
		return invalidInput("void authorization requires authorization id and request id")
	}
	return c.call(ctx, http.MethodPost,
		"/v2/payments/authorizations/"+authorizationID+"/void",
		requestID, map[string]any{}, nil)
}

// ReauthorizeAuthorization은 honor period 밖에서 provider가 허용할 때 새
// authorization resource를 만든다. 호출자는 새 ID를 저장하고 fresh GET으로 대사한다.
func (c *Client) ReauthorizeAuthorization(
	ctx context.Context,
	input ReauthorizeAuthorizationInput,
) (Authorization, error) {
	input.AuthorizationID = strings.TrimSpace(input.AuthorizationID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Currency = strings.TrimSpace(input.Currency)
	input.OrderID = strings.TrimSpace(input.OrderID)
	input.PayeeMerchant = strings.TrimSpace(input.PayeeMerchant)
	if input.AuthorizationID == "" || input.RequestID == "" ||
		input.AmountMinor <= 0 || input.Currency != "USD" ||
		input.OrderID == "" || input.PayeeMerchant == "" {
		return Authorization{}, invalidInput(
			"reauthorize requires authorization/order/payee ids, USD and a positive exact remaining amount",
		)
	}
	payload := map[string]any{
		"amount": Amount{
			CurrencyCode: input.Currency,
			Value:        MinorToValue(input.AmountMinor),
		},
	}
	var parsed authorizationResponse
	if err := c.call(ctx, http.MethodPost,
		"/v2/payments/authorizations/"+input.AuthorizationID+"/reauthorize",
		input.RequestID, payload, &parsed); err != nil {
		return Authorization{}, err
	}
	authorization, err := parsed.toAuthorization()
	if err != nil {
		// A malformed 2xx can still have created a fresh provider resource.
		// Preserve its identity so the caller can store UNKNOWN and reconcile by GET.
		return Authorization{ID: strings.TrimSpace(parsed.ID)}, err
	}
	if authorization.ID == input.AuthorizationID {
		return authorization, nonconforming(
			"reauthorization did not return a fresh authorization id",
		)
	}
	if authorization.Status != "CREATED" {
		return authorization, nonconforming(
			"reauthorization response status is not CREATED",
		)
	}
	if authorization.AmountMinor != input.AmountMinor ||
		authorization.Currency != input.Currency ||
		authorization.ParentOrderID != input.OrderID ||
		authorization.PayeeMerchant != input.PayeeMerchant {
		return authorization, nonconforming(
			"reauthorization response does not match exact order, payee, or remaining amount",
		)
	}
	return authorization, nil
}

type Refund struct {
	ID              string
	Status          string
	AmountMinor     int64
	Currency        string
	ParentCaptureID string
	InvoiceID       string
}

type refundResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Amount    Amount `json:"amount"`
	InvoiceID string `json:"invoice_id"`
	Links     []struct {
		Href string `json:"href"`
		Rel  string `json:"rel"`
	} `json:"links"`
}

func (r refundResponse) toRefund() (Refund, error) {
	refund := Refund{
		ID: strings.TrimSpace(r.ID), Status: strings.TrimSpace(r.Status),
		Currency:  strings.TrimSpace(r.Amount.CurrencyCode),
		InvoiceID: strings.TrimSpace(r.InvoiceID),
	}
	for _, link := range r.Links {
		if link.Rel != "up" {
			continue
		}
		captureID, err := linkedPayPalResourceID(link.Href, "captures")
		if err != nil {
			return refund, nonconforming("refund parent capture link is invalid")
		}
		if refund.ParentCaptureID != "" && refund.ParentCaptureID != captureID {
			return refund, nonconforming("refund has conflicting parent capture links")
		}
		refund.ParentCaptureID = captureID
	}
	if refund.ID == "" || refund.Status == "" || r.Amount.Value == "" {
		return refund, nonconforming("refund response is missing identity, status, or amount")
	}
	minor, err := ValueToMinor(r.Amount.Value)
	if err != nil {
		return refund, nonconforming("refund response amount is invalid: %v", err)
	}
	refund.AmountMinor = minor
	return refund, nil
}

func linkedPayPalResourceID(href, collection string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(href))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("invalid PayPal resource link")
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 4 || parts[0] != "v2" || parts[1] != "payments" ||
		parts[2] != collection || parts[3] == "" {
		return "", errors.New("unexpected PayPal resource path")
	}
	resourceID, err := url.PathUnescape(parts[3])
	if err != nil || strings.TrimSpace(resourceID) == "" || strings.Contains(resourceID, "/") {
		return "", errors.New("invalid PayPal resource identity")
	}
	return resourceID, nil
}

func linkedPayPalCheckoutOrderID(href string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(href))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("invalid PayPal order link")
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 4 || parts[0] != "v2" || parts[1] != "checkout" ||
		parts[2] != "orders" || parts[3] == "" {
		return "", errors.New("unexpected PayPal order path")
	}
	orderID, err := url.PathUnescape(parts[3])
	if err != nil || strings.TrimSpace(orderID) == "" || strings.Contains(orderID, "/") {
		return "", errors.New("invalid PayPal order identity")
	}
	return orderID, nil
}

// RefundCapture는 수납된 Capture에서 amountMinor만큼 환불한다. amount는 부분·누적
// 환불을 지원하며 합은 수납액을 넘을 수 없다(provider가 거절).
func (c *Client) RefundCapture(
	ctx context.Context,
	captureID, requestID string,
	amountMinor int64,
) (Refund, error) {
	captureID = strings.TrimSpace(captureID)
	requestID = strings.TrimSpace(requestID)
	if captureID == "" || requestID == "" || len(requestID) > 127 || amountMinor <= 0 {
		return Refund{}, invalidInput("refund requires capture id, bounded request id, and positive amount")
	}
	payload := map[string]any{
		"amount":     Amount{CurrencyCode: "USD", Value: MinorToValue(amountMinor)},
		"invoice_id": requestID,
	}
	var parsed refundResponse
	err := c.call(ctx, http.MethodPost,
		"/v2/payments/captures/"+captureID+"/refund",
		requestID, payload, &parsed)
	if err != nil {
		return Refund{}, err
	}
	refund, err := parsed.toRefund()
	if err != nil {
		return refund, err
	}
	if refund.Currency != "USD" || refund.AmountMinor != amountMinor ||
		refund.ParentCaptureID != captureID || refund.InvoiceID != requestID ||
		refund.InvoiceID == "" {
		return refund, nonconforming(
			"refund response does not match exact capture, amount, or operation metadata",
		)
	}
	return refund, nil
}

func (c *Client) GetRefund(ctx context.Context, refundID string) (Refund, error) {
	var parsed refundResponse
	err := c.call(ctx, http.MethodGet,
		"/v2/payments/refunds/"+strings.TrimSpace(refundID), "", nil, &parsed)
	if err != nil {
		return Refund{}, err
	}
	return parsed.toRefund()
}

// WebhookVerification은 raw body와 transmission header 원문으로 서명을 검증한다.
// body를 재직렬화하지 않는다.
type WebhookVerification struct {
	AuthAlgo         string
	CertURL          string
	TransmissionID   string
	TransmissionSig  string
	TransmissionTime string
	WebhookID        string
	RawEvent         []byte
}

func (c *Client) VerifyWebhookSignature(
	ctx context.Context,
	input WebhookVerification,
) (bool, error) {
	if input.TransmissionID == "" || input.TransmissionSig == "" ||
		input.TransmissionTime == "" || input.WebhookID == "" || len(input.RawEvent) == 0 {
		return false, nil
	}
	payload := map[string]any{
		"auth_algo":         input.AuthAlgo,
		"cert_url":          input.CertURL,
		"transmission_id":   input.TransmissionID,
		"transmission_sig":  input.TransmissionSig,
		"transmission_time": input.TransmissionTime,
		"webhook_id":        input.WebhookID,
		"webhook_event":     json.RawMessage(input.RawEvent),
	}
	var parsed struct {
		VerificationStatus string `json:"verification_status"`
	}
	err := c.call(ctx, http.MethodPost,
		"/v1/notifications/verify-webhook-signature", "", payload, &parsed)
	if err != nil {
		return false, err
	}
	return parsed.VerificationStatus == "SUCCESS", nil
}

// call은 공통 HTTP 실행이다. write(requestID 존재) 전송 뒤의 네트워크 오류와
// 5xx는 ErrOutcomeUnknown으로 분류해 호출자가 대사하게 한다.
func (c *Client) call(
	ctx context.Context,
	method, path, requestID string,
	payload any,
	out any,
) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.config.BaseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	if requestID != "" {
		request.Header.Set("PayPal-Request-Id", requestID)
	}
	if method != http.MethodGet && requestID != "" && out != nil {
		// AUTHORIZE/Capture/Reauthorize의 response resource를 같은 write 결과와
		// 대조한다. accepted money truth는 여전히 fresh GET/webhook 대사가 소유한다.
		request.Header.Set("Prefer", "return=representation")
	}
	// money-write(요청 ID 존재)는 ExternalEffect로 표시해, 요청이 이미 나간 뒤의
	// 모호한 실패를 shared httpclient가 EXTERNAL_EFFECT_UNKNOWN으로 구분하게 한다.
	isWrite := method != http.MethodGet
	effect := sharedhttpclient.ReadOnly
	if isWrite && requestID != "" {
		effect = sharedhttpclient.ExternalEffect
	}
	response, err := sharedhttpclient.Do(ctx, c.http, request, effect)
	if err != nil {
		if classified, ok := fault.As(err); ok &&
			classified.Code == fault.ExternalEffectUnknown {
			return fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
		}
		return fmt.Errorf("paypal %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	raw, err := sharedhttpclient.ReadBody(response.Body, 1<<20)
	if err != nil {
		if isWrite && requestID != "" {
			return fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
		}
		return fmt.Errorf("paypal %s %s read: %w", method, path, err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if out != nil {
			if len(raw) == 0 {
				if isWrite && requestID != "" {
					return fmt.Errorf("%w: paypal %s %s returned an empty success body",
						ErrNonconformingResponse, method, path)
				}
				return fmt.Errorf("paypal %s %s returned an empty success body", method, path)
			}
			if err := json.Unmarshal(raw, out); err != nil {
				if isWrite && requestID != "" {
					return fmt.Errorf("%w: paypal %s %s decode: %v",
						ErrNonconformingResponse, method, path, err)
				}
				return fmt.Errorf("paypal %s %s decode: %w", method, path, err)
			}
		}
		return nil
	}
	if response.StatusCode >= 500 && isWrite && requestID != "" {
		return fmt.Errorf("%w: status %d", ErrOutcomeUnknown, response.StatusCode)
	}
	apiError := &APIError{StatusCode: response.StatusCode}
	var parsed struct {
		Name    string `json:"name"`
		DebugID string `json:"debug_id"`
		Details []struct {
			Issue string `json:"issue"`
		} `json:"details"`
	}
	if json.Unmarshal(raw, &parsed) == nil {
		apiError.Name = parsed.Name
		apiError.DebugID = parsed.DebugID
		if len(parsed.Details) > 0 {
			apiError.IssueCode = parsed.Details[0].Issue
		}
	}
	return apiError
}
