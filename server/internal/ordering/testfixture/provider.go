package testfixture

import (
	"context"
	"errors"
	"fmt"
	paymentapp "github.com/vitlane/vitlane/server/internal/ordering/payment/app"
	"github.com/vitlane/vitlane/server/internal/ordering/payment/infra/paypal"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"sync"
	"time"
)

type Clock struct{ Time time.Time }

func (c *Clock) Now() time.Time { return c.Time }

type Provider struct {
	paymentapp.ProviderClient
	mu           sync.Mutex
	Captures     []paypal.CaptureAuthorizationInput
	Capture      paypal.Capture
	RefundKeys   []string
	Refund       paypal.Refund
	RefundStatus string
	Voided       bool
	Unknown      bool
	OnCapture    func(context.Context)
}

func (p *Provider) GetOrder(context.Context, string) (paypal.Order, error) {
	return paypal.Order{}, nil
}
func (p *Provider) AuthorizeOrder(context.Context, string, string) (paypal.Order, error) {
	return paypal.Order{}, errors.New("unexpected full-order authorization")
}
func (p *Provider) GetAuthorizedOrder(context.Context, string) (paypal.Order, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	o := paypal.Order{}
	if p.Capture.ID != "" {
		o.Captures = []paypal.Capture{p.Capture}
	}
	return o, nil
}
func (p *Provider) GetAuthorization(_ context.Context, id string) (paypal.Authorization, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	status := "CREATED"
	if p.Voided {
		status = "VOIDED"
	}
	return paypal.Authorization{ID: id, Status: status, AmountMinor: 2138, Currency: "USD", ParentOrderID: "PAYPAL-ORDER-FAILED-SIBLING", PayeeMerchant: "MERCHANT-1"}, nil
}
func (p *Provider) ReauthorizeAuthorization(context.Context, paypal.ReauthorizeAuthorizationInput) (paypal.Authorization, error) {
	return paypal.Authorization{}, errors.New("unexpected reauthorization")
}
func (p *Provider) CaptureAuthorization(ctx context.Context, in paypal.CaptureAuthorizationInput) (paypal.Capture, error) {
	if sharedapp.InTransaction(ctx) {
		panic("provider called inside DB transaction")
	}
	if p.OnCapture != nil {
		p.OnCapture(ctx)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Captures = append(p.Captures, in)
	p.Capture = paypal.Capture{ID: "REDUCER-CAPTURE", Status: "COMPLETED", AmountMinor: in.AmountMinor, Currency: in.Currency, InvoiceID: in.InvoiceID, FinalCapture: in.FinalCapture}
	if p.Unknown {
		return paypal.Capture{}, paypal.ErrOutcomeUnknown
	}
	return p.Capture, nil
}
func (p *Provider) GetCapture(context.Context, string) (paypal.Capture, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Unknown {
		return paypal.Capture{}, paypal.ErrOutcomeUnknown
	}
	return p.Capture, nil
}
func (p *Provider) VoidAuthorization(ctx context.Context, id, key string) error {
	if sharedapp.InTransaction(ctx) {
		panic("void inside TX")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Voided = true
	return nil
}
func (p *Provider) RefundCapture(ctx context.Context, id, key string, amount int64) (paypal.Refund, error) {
	if sharedapp.InTransaction(ctx) {
		panic("Refund inside TX")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.RefundKeys = append(p.RefundKeys, key)
	status := p.RefundStatus
	if status == "" {
		status = "COMPLETED"
	}
	p.Refund = paypal.Refund{ID: fmt.Sprintf("REDUCER-REFUND-%d", len(p.RefundKeys)), Status: status, AmountMinor: amount, Currency: "USD", ParentCaptureID: id, InvoiceID: key}
	return p.Refund, nil
}
func (p *Provider) GetRefund(context.Context, string) (paypal.Refund, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Refund, nil
}
