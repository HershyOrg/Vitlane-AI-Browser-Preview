CREATE TABLE research_daily_exchange_rate (
 pair TEXT PRIMARY KEY CHECK(pair='USD/KRW'),
 rate TEXT,
 as_of DATE,
 observed_at TIMESTAMPTZ,
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
 CHECK ((rate IS NULL AND as_of IS NULL AND observed_at IS NULL) OR (rate IS NOT NULL AND as_of IS NOT NULL AND observed_at IS NOT NULL))
);
INSERT INTO research_daily_exchange_rate(pair) VALUES('USD/KRW');
