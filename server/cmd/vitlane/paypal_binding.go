package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	paymentdomain "github.com/vitlane/vitlane/server/internal/ordering/payment/domain"
	paymentpostgres "github.com/vitlane/vitlane/server/internal/ordering/payment/infra/postgres"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

// paypalClientFingerprint는 client ID의 non-secret 지문이다. 원문 credential을
// 저장하지 않으면서 binding과 배포 설정의 일치를 startup에서 검증한다.
func paypalClientFingerprint(clientID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(clientID)))
	return hex.EncodeToString(sum[:8])
}

// runPayPalBindingCommand는 `vitlane paypal-binding register` 서브커맨드다.
// 운영자가 PayPal merchant 화면에서 직접 확인한 non-secret merchant ID를 감사
// 정보와 함께 등록한다(ADR-0050 — email로 merchant를 추론하지 않는다).
func runPayPalBindingCommand(ctx context.Context, args []string, logger *slog.Logger) error {
	if len(args) == 0 || args[0] != "register" {
		return fmt.Errorf("usage: vitlane paypal-binding register --merchant-id <id> --verified-by <operator> [--environment SANDBOX] [--evidence <note>]")
	}
	flags := flag.NewFlagSet("paypal-binding register", flag.ContinueOnError)
	environment := flags.String("environment", "SANDBOX", "PayPal environment (SANDBOX|LIVE)")
	merchantID := flags.String("merchant-id", "", "merchant ID confirmed on the PayPal dashboard")
	verifiedBy := flags.String("verified-by", "", "operator who confirmed the merchant ID")
	evidence := flags.String("evidence", "", "where/how the merchant ID was confirmed")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if strings.TrimSpace(*merchantID) == "" || strings.TrimSpace(*verifiedBy) == "" {
		return fmt.Errorf("--merchant-id and --verified-by are required")
	}
	environmentName := strings.ToUpper(strings.TrimSpace(*environment))
	if environmentName != "SANDBOX" && environmentName != "LIVE" {
		return fmt.Errorf("--environment must be SANDBOX or LIVE")
	}
	clientID := strings.TrimSpace(os.Getenv("PAYPAL_" + environmentName + "_CLIENT_ID"))
	webhookID := strings.TrimSpace(os.Getenv("PAYPAL_" + environmentName + "_WEBHOOK_ID"))
	if clientID == "" || webhookID == "" {
		return fmt.Errorf("PAYPAL_%s_CLIENT_ID and PAYPAL_%s_WEBHOOK_ID must be set",
			environmentName, environmentName)
	}
	databaseURL := env("DATABASE_URL", "postgres://vitlane:vitlane@127.0.0.1:5432/vitlane?sslmode=disable")
	database, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer database.Close()
	// binding은 서버 boot의 fail-close 선행조건이라 첫 기동 전에 등록될 수
	// 있어야 한다 — 멱등 migrate로 스키마를 보장한다.
	if err := database.Migrate(ctx, env("MIGRATIONS_DIR", "migrations")); err != nil {
		return fmt.Errorf("migrate before binding registration: %w", err)
	}
	repository := paymentpostgres.NewRepository(database)
	now := time.Now().UTC()
	binding := paymentdomain.AccountBinding{
		Environment:         environmentName,
		MerchantID:          strings.TrimSpace(*merchantID),
		ClientIDFingerprint: paypalClientFingerprint(clientID),
		WebhookID:           webhookID,
		VerifiedBy:          strings.TrimSpace(*verifiedBy),
		EvidenceNote:        strings.TrimSpace(*evidence),
		VerifiedAt:          now,
	}
	if err := repository.UpsertBinding(ctx, binding, now); err != nil {
		return err
	}
	logger.Info("paypal account binding registered",
		"event", "payment.paypal.binding_registered",
		"environment", binding.Environment,
		"merchant_id", binding.MerchantID,
		"client_id_fingerprint", binding.ClientIDFingerprint,
		"webhook_id", binding.WebhookID,
		"verified_by", binding.VerifiedBy,
	)
	return nil
}
