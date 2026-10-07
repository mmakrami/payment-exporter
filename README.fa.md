# Payment Exporter — راهنمای اجرا

این exporter برای مانیتورینگ HTTP سرویس‌هاست. آدرس‌ها، متد درخواست، هدر، بدنه و
**کدهای دقیق پاسخ سالم** از فایل YAML خوانده می‌شوند. پورت پیش‌فرض **9106** است.
برای عوض کردن سرویس‌ها نیازی به تغییر سورس یا ساخت دوبارهٔ ایمیج ندارید.

## اجرای مستقیم از GitHub

```bash
git clone https://github.com/mmakrami/payment-exporter.git
cd payment-exporter
cp config.example.yml config.yml
# آدرس‌ها و کدهای سالم واقعی را در config.yml تنظیم کنید.
docker compose build
docker compose run --rm payment-exporter --config.check
docker compose up -d
curl http://localhost:9106/-/healthy
```

برای این روش نصب Go لازم نیست؛ Docker ایمیج را می‌سازد. برای نسخهٔ MTR، در هر
سه دستور Compose از `docker compose -f compose.mtr.yaml` استفاده کنید و MTR را
فقط برای سرویس‌های موردنظر فعال کنید.

برای اجرا با ایمیج آماده، بستهٔ **quickstart** را از
[GitHub Releases](https://github.com/mmakrami/payment-exporter/releases) دریافت و
استخراج کنید. این بسته فایل‌های لازم برای اجرا، هر دو ایمیج، نمونه‌های Prometheus
و داشبورد Grafana را کنار هم دارد. سپس مطابق بخش بعد ایمیج‌ها را بارگذاری کنید.

## اجرای سریع با ایمیج آماده

ابتدا آرشیو ایمیج‌ها را کنار فایل‌های پروژه قرار دهید:

```bash
docker load -i payment-exporter-1.0.0-images.tar.gz
cp config.example.yml config.yml
```

فایل `config.yml` را برای سرویس‌های خودتان تنظیم کنید:

```yaml
global:
  timeout: 10s
  max_concurrency: 16

providers:
  - name: MyHealthEndpoint
    url: https://your-domain.example/health
    method: GET
    expected_status_codes: [200]

  - name: MyTokenEndpoint
    url: https://your-domain.example/token
    method: POST
    headers:
      Content-Type: application/json
    body: '{}'
    expected_status_codes: [401]
    timeout: 8s
```

کدهای بالا نمونه‌اند؛ برای هر سرویس کد سالم را مطابق قرارداد واقعی آن وارد کنید.
اگر مقدار `[401]` تنظیم شود، فقط `401` سالم است. `200` و بقیهٔ کدها برای همان
سرویس سالم محسوب نمی‌شوند. چند کد مجاز را به شکل `[200, 204]` وارد کنید.

سپس اعتبار تنظیمات را بررسی کنید و کانتینر را اجرا کنید:

```bash
docker run --rm \
  -v "$PWD/config.yml:/etc/payment-exporter/config.yml:ro" \
  paystar/payment-exporter:1.0.0 \
  --config.file=/etc/payment-exporter/config.yml --config.check

docker run -d --name payment-exporter --restart unless-stopped \
  --read-only --cap-drop=ALL --security-opt=no-new-privileges:true \
  --pids-limit=128 --memory=256m --cpus=1 \
  -p 9106:9106 \
  -v "$PWD/config.yml:/etc/payment-exporter/config.yml:ro" \
  paystar/payment-exporter:1.0.0

curl http://localhost:9106/-/healthy
curl http://localhost:9106/metrics
```

اگر Docker Compose v2 نصب دارید، فایل `compose.yaml` آماده است:

```bash
docker compose up -d
docker compose logs --tail=50 payment-exporter
```

بعد از تغییر تنظیمات یا گواهی‌ها، کانتینر را restart کنید:

```bash
docker restart payment-exporter
```

در Compose از `docker compose restart payment-exporter` استفاده کنید.

## اتصال به Prometheus

```yaml
scrape_configs:
  - job_name: payment-exporter
    scrape_interval: 30s
    scrape_timeout: 25s
    static_configs:
      - targets: ['EXPORTER_HOST:9106']
```

به جای `EXPORTER_HOST`، IP یا hostname قابل دسترس لپ‌تاپ/سرور exporter را وارد
کنید. اگر Prometheus و exporter در یک شبکهٔ Compose باشند، نام سرویس
`payment-exporter:9106` قابل استفاده است.

هر درخواست `/metrics` یک بررسی تازه برای همهٔ سرویس‌های تنظیم‌شده انجام می‌دهد؛
فاصلهٔ بررسی‌ها را Prometheus تعیین می‌کند. مشاهدهٔ دستی این مسیر نیز probe
اجرا می‌کند. مسیر `/-/healthy` به سرویس‌های مقصد درخواست نمی‌فرستد.

`up` خود Prometheus وضعیت دسترسی به exporter است.
`payment_provider_up{provider="..."}` وضعیت بررسی HTTP آن سرویس است. سالم بودن
این بررسی، موفقیت پرداخت یا صحت محتوای JSON/XML را اثبات نمی‌کند.

برای بیشتر از ۱۶ سرویس یا timeoutهای طولانی، `scrape_timeout` را متناسب با
تعداد دسته‌های بررسی بیشتر کنید. راهنمای کامل محاسبه در `README.md` است.
درخواست هم‌زمان دوم `/metrics` پاسخ `503` می‌گیرد؛ برای دو Prometheus موازی،
دو نمونهٔ exporter اجرا کنید.

## گواهی کلاینت و MTR

mTLS حفظ شده است. برای فایل PEM ترکیبی:

```yaml
tls:
  cert_file: /run/secrets/provider.pem
  key_file: /run/secrets/provider.pem
```

فایل را هنگام اجرا به همین مسیر mount کنید. کاربر کانتینر `65532:65532` است و
باید اجازهٔ خواندن فایل را داشته باشد. گواهی و private key داخل ایمیج نیستند.
گزینهٔ `ca_file` برای CA اختصاصی و `server_name` برای نام TLS نیز موجود است.

MTR پیش‌فرض خاموش است. برای فعال کردنش، تنظیم زیر را داخل همان سرویس اضافه کنید:

```yaml
mtr:
  enabled: true
  protocol: tcp
  cycles: 3
  timeout: 15s
```

سپس از ایمیج `paystar/payment-exporter:1.0.0-mtr` با `--cap-add=NET_RAW` استفاده
کنید؛ `no-new-privileges` برای این نسخه نباید فعال باشد. فایل مستقل
`compose.mtr.yaml` تنظیم صحیح را دارد:

```bash
docker compose -f compose.mtr.yaml up -d
```

MTR به `privileged` یا `NET_ADMIN` نیاز ندارد. نتیجهٔ آن مستقل از سلامت HTTP است.
اگر مقصد قابل اندازه‌گیری نباشد، `mtr_success=0` ثبت می‌شود و عدد قبلی تکرار
نمی‌شود. `protocol: tcp` پورت URL را بررسی می‌کند؛ `icmp` تشخیص مسیر با ICMP است.

جزئیات تمام گزینه‌ها، متریک‌ها، مدیریت هدر محرمانه از محیط و دستور build در
`README.md` آمده است. تغییرات نسبت به نسخهٔ قبلی در `MIGRATION.md` نوشته شده‌اند.

## داشبورد Grafana

فایل [grafana/payment-exporter.dashboard.json](grafana/payment-exporter.dashboard.json)
را از مسیر **Dashboards → New → Import** وارد کنید. datasource پرومتوس و job
اکسپورتـر خودتان را انتخاب کنید؛ نام پیش‌فرض job برابر `payment-exporter` است.
این پروژه Grafana یا Prometheus را نصب نمی‌کند.

داشبورد وضعیت لحظه‌ای، جدول خرابی‌ها، تاریخچهٔ سلامت، تأخیر p95، نسبت موفقیت
probeها، کدهای HTTP و علت خطاها را نمایش می‌دهد. بخش‌های MTR و منابع exporter
پیش‌فرض جمع شده‌اند. پلاگین اضافی یا recording rule لازم ندارد.

دادهٔ قدیمی یا قطع scrape سالم فرض نمی‌شود؛ کدهای 401 و 404 ممکن است مطابق
config سالم باشند. شکست MTR نیز به معنی صفر درصد packet loss نیست. نسبت موفقیت
مربوط به درخواست‌های مانیتورینگ است، نه تراکنش پرداخت یا SLA.

ساختار فایل و تنظیمات پنل‌ها با schema رسمی Grafana و کوئری‌ها با `promtool`
بررسی شده‌اند. import و نمایش نهایی در Grafana هنوز تست نشده است.
