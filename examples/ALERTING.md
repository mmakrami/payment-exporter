# Alerting / راهنمای الرت‌ها

Copy `examples/alerts.yml` into a rule directory already loaded by your
Prometheus, such as `/opt/prometheus/prometheus/alert_rules/payment_provider.yml`.
Replace the previous provider rule file rather than keeping both active.
The sample `examples/prometheus.yml` loads the file at
`/etc/prometheus/payment-exporter-alerts.yml`; set that path to your actual file.

Keep `scrape_interval: 30s`. The rule group evaluates every 30 seconds. Configure
the existing Alertmanager address under `alerting.alertmanagers`; rules alone
create Prometheus alerts but do not select a notification destination. Keep your
existing receivers, routes and notification credentials in Alertmanager.

| Alert | Condition | Severity |
|---|---|---|
| PaymentProviderDown5x | At least five failed samples and no successful sample in the last 150s; current probe also failed | critical |
| PaymentProviderErrorRateHigh | More than 60% failures among observed samples in a rolling 5m window, continuously for 5m | critical |
| PaymentProviderHighLatency | Estimated p50 probe latency greater than 3s, continuously for 5m | critical |
| PaymentExporterUnavailable | Exporter scrape failed continuously for 2m | critical |
| PaymentMTRFailed | Enabled MTR measurement failed continuously for 5m | warning |

The three provider alerts exclude `Sunny`, matching the previous configuration.
Remove `provider!="Sunny"` to include it. These rules work with any exporter job
name, including `payment-provider-exporter-v2`; instances are kept separate.
All provider/MTR conditions require a successful exporter scrape. Exporter
discovery uses build metadata observed within 24 hours, so that alert requires
at least one successful scrape first. Customize a static `up{job="..."} == 0`
rule if you also need to detect a target that has never successfully started.

Thresholds follow the previous expressions, not the contradictory comments:
**60%, critical, p50 >3s**. Counts concern HTTP monitoring attempts, not payment
transactions. The percentile includes failed probes. Adjust the 150s window if
you change the scrape interval; five 30s-spaced failures can first be observed
120s after the first failure, depending on evaluation timing.

Validate before reloading your Prometheus:

```sh
promtool check rules /opt/prometheus/prometheus/alert_rules/payment_provider.yml
promtool check config /path/to/prometheus.yml
```

Reload using the method already used by your deployment, then check Prometheus
**Status → Rule health / Rules** and **Alerts**. An Alertmanager receiver must be
configured for messages to be delivered. This repository does not send messages
or modify your running monitoring server.

**فارسی:** سه الرت اصلی همان آستانه‌های واقعی قوانین قبلی را دارند و Sunny از
آن‌ها مستثناست. «پنج خطای پیاپی» فقط با پنج نمونهٔ واقعی ناموفق و بدون نمونهٔ
موفق در پنجره فعال می‌شود؛ قطع دریافت متریک، خرابی provider فرض نمی‌شود.
شرط دوم بیش از ۶۰ درصد خطا و شرط سوم p50 بالاتر از ۳ ثانیه است و هر دو باید
پنج دقیقه برقرار بمانند. برای ارسال پیام، اتصال Prometheus به Alertmanager و
receiverهای فعلی آن لازم است. قوانین را جایگزین فایل قبلی و پس از اعتبارسنجی
Prometheus را reload کنید.

See [Prometheus rule testing](https://prometheus.io/docs/prometheus/latest/configuration/unit_testing_rules/)
for the validation method used in this project.
