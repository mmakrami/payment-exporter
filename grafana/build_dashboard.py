#!/usr/bin/env python3
"""Build a portable Classic Grafana dashboard using only built-in panels."""

import copy
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DS = {"type": "prometheus", "uid": "${datasource}"}
KEY = "job, instance, provider"
SELECT = 'job=~"${job:regex}", instance=~"${instance:regex}", provider=~"${provider:regex}"'
EXPORTER_SELECT = 'job=~"${job:regex}", instance=~"${instance:regex}"'
GREEN, RED, AMBER, BLUE, GRAY, TEAL = "green", "red", "orange", "blue", "#8F9CB3", "#56D3C9"


def metric(name, provider=True):
    return name + "{" + (SELECT if provider else EXPORTER_SELECT) + "}"


def grouped(expr):
    return f"max by ({KEY}) ({expr})"


UP = f"max by (job, instance) ({metric('up', False)})"
STAMP = grouped(metric("payment_provider_last_probe_timestamp_seconds"))
FRESH = f"on (job, instance) ({UP} == 1)"
AGE = f"(time() - {STAMP})"
FRESH_FILTER = f"and {FRESH} and on ({KEY}) ({AGE} <= $freshness) and on ({KEY}) ({AGE} >= -5)"
HTTP = f"({grouped(metric('payment_provider_up'))} {FRESH_FILTER})"
KNOWN = grouped(f"last_over_time({metric('payment_provider_up')}[$__range])")
# Numeric severity allows native table sorting: failures, scrape loss, stale, healthy.
STATE = f"((3 * (1 - {HTTP})) or ((2 + 0 * {KNOWN}) and on (job, instance) ({UP} == 0)) or (1 + 0 * {KNOWN}))"
COUNT = metric("payment_provider_latency_seconds_count")
ERRORS = metric("payment_provider_probe_errors_total")
RATE_ERRORS = f"sum by ({KEY}) (rate({ERRORS}[$__rate_interval]))"
RATE_PROBES = f"sum by ({KEY}) (rate({COUNT}[$__rate_interval]))"
WINDOW_ERRORS = f"sum by ({KEY}) (increase({ERRORS}[$__range]))"
WINDOW_PROBES = f"sum by ({KEY}) (increase({COUNT}[$__range]))"
WINDOW_SUCCESS = f"(clamp_max(clamp_min(1 - {WINDOW_ERRORS} / {WINDOW_PROBES}, 0), 1) and on ({KEY}) ({WINDOW_PROBES} > 0))"
RECENT_SUCCESS = f"((clamp_max(clamp_min(1 - {RATE_ERRORS} / {RATE_PROBES}, 0), 1) and on ({KEY}) ({RATE_PROBES} > 0)) and {FRESH})"
DURATION = f"({grouped(metric('payment_provider_probe_duration_seconds'))} {FRESH_FILTER})"
BUDGET = f"({DURATION} / on ({KEY}) {grouped(metric('payment_provider_probe_timeout_seconds'))})"
P95 = f"histogram_quantile(0.95, sum by (le) (rate({metric('payment_provider_latency_seconds_bucket')}[$__rate_interval])))"


def mapping(values, null="NO DATA"):
    result = [{"type": "value", "options": {str(key): {"text": text, "color": color, "index": i} for i, (key, (text, color)) in enumerate(values.items())}}]
    if null:
        result.append({"type": "special", "options": {"match": "null", "result": {"text": null, "color": GRAY, "index": len(values)}}})
        result.append({"type": "special", "options": {"match": "nan", "result": {"text": null, "color": GRAY, "index": len(values) + 1}}})
    return result


def thresholds(base=GREEN, *changes):
    return {"mode": "absolute", "steps": [{"color": base, "value": None}] + [{"color": color, "value": value} for value, color in changes]}


def target(expr, ref="A", instant=False, table=False, legend="{{provider}} · {{instance}}"):
    return {"datasource": copy.deepcopy(DS), "editorMode": "code", "expr": expr, "refId": ref,
            "format": "table" if table else "time_series", "instant": instant, "range": not instant,
            "legendFormat": legend, "interval": "30s", "exemplar": False}


panels = []
next_id = 0


def panel(kind, title, x, y, w, h, expr=None, description="", unit="short", instant=False):
    global next_id
    next_id += 1
    p = {"id": next_id, "type": kind, "title": title, "description": description,
         "gridPos": {"x": x, "y": y, "w": w, "h": h}, "datasource": copy.deepcopy(DS),
         "fieldConfig": {"defaults": {"unit": unit, "noValue": "—", "mappings": [],
             "color": {"mode": "palette-classic"}, "thresholds": thresholds()}, "overrides": []},
         "options": {}, "targets": []}
    if expr:
        p["targets"] = [target(expr, instant=instant)]
    if not instant:
        p["maxDataPoints"] = 600
    return p


def stat(title, x, expr, description, unit="short", color=BLUE, limits=None, maps=None):
    p = panel("stat", title, x, 3, 4, 4, expr, description, unit, True)
    p["options"] = {"colorMode": "value", "graphMode": "none", "justifyMode": "center",
                    "orientation": "auto", "textMode": "value", "wideLayout": True,
                    "reduceOptions": {"values": False, "calcs": ["lastNotNull"], "fields": ""}}
    d = p["fieldConfig"]["defaults"]
    d["decimals"] = 1 if unit in {"s", "percentunit"} else 0
    d["color"] = {"mode": "thresholds"} if limits or maps else {"mode": "fixed", "fixedColor": color}
    d["thresholds"] = limits or thresholds(color)
    d["mappings"] = maps or mapping({}, "NO DATA")
    panels.append(p)


def text(title, y, h, content):
    p = panel("text", title, 0, y, 24, h)
    p.pop("datasource")
    p.pop("fieldConfig")
    p.pop("maxDataPoints", None)
    p["options"] = {"mode": "markdown", "content": content}
    return p


def row(title, y, collapsed=False, children=None):
    global next_id
    next_id += 1
    return {"id": next_id, "type": "row", "title": title, "collapsed": collapsed,
            "gridPos": {"x": 0, "y": y, "w": 24, "h": 1}, "panels": children or []}


def timeline(title, y, h, expr, description, raw_maps):
    p = panel("state-timeline", title, 0, y, 24, h, expr, description)
    p["maxDataPoints"] = 1500
    p["fieldConfig"]["defaults"].update({"mappings": mapping(raw_maps, "UNKNOWN"),
        "color": {"mode": "thresholds"}, "custom": {"lineWidth": 0, "fillOpacity": 85,
        "spanNulls": False, "hideFrom": {"legend": False, "tooltip": False, "viz": False}}})
    p["options"] = {"mergeValues": True, "showValue": "never", "rowHeight": 0.85,
                    "alignValue": "left", "legend": {"showLegend": True, "displayMode": "list", "placement": "bottom"},
                    "tooltip": {"mode": "single", "sort": "none"}}
    return p


def series(title, x, y, w, expr, description, unit="s", fixed=None, limits=None, legend="{{provider}} · {{instance}}"):
    p = panel("timeseries", title, x, y, w, 8, expr, description, unit)
    p["targets"][0]["legendFormat"] = legend
    d = p["fieldConfig"]["defaults"]
    d.update({"min": 0, "custom": {"drawStyle": "line", "lineInterpolation": "linear", "lineWidth": 2,
        "fillOpacity": 8, "gradientMode": "none", "showPoints": "never", "pointSize": 4,
        "spanNulls": False, "axisPlacement": "auto", "axisLabel": "", "axisColorMode": "text",
        "scaleDistribution": {"type": "linear"}, "hideFrom": {"legend": False, "tooltip": False, "viz": False},
        "stacking": {"mode": "none", "group": "A"}, "thresholdsStyle": {"mode": "off"}}})
    if unit == "percentunit":
        d["max"] = 1
        d["decimals"] = 1
    if fixed:
        d["color"] = {"mode": "fixed", "fixedColor": fixed}
    if limits:
        d["thresholds"] = limits
        d["custom"]["thresholdsStyle"] = {"mode": "dashed"}
    p["options"] = {"legend": {"showLegend": True, "displayMode": "table", "placement": "bottom", "calcs": ["lastNotNull", "max"]},
                    "tooltip": {"mode": "multi", "sort": "desc"}}
    return p


def bars(title, x, y, expr, description, unit="s", budget=False):
    p = panel("bargauge", title, x, y, 12, 7, expr, description, unit, True)
    p["fieldConfig"]["defaults"].update({"min": 0, "decimals": 1})
    if budget:
        p["fieldConfig"]["defaults"].update({"max": 1, "color": {"mode": "thresholds"},
                                           "thresholds": thresholds(BLUE, (0.8, AMBER), (1, RED))})
    else:
        p["fieldConfig"]["defaults"]["color"] = {"mode": "fixed", "fixedColor": BLUE}
    p["options"] = {"orientation": "horizontal", "displayMode": "basic", "valueMode": "text",
                    "namePlacement": "left", "showUnfilled": True, "sizing": "auto",
                    "minVizHeight": 16, "maxVizHeight": 36,
                    "reduceOptions": {"values": False, "calcs": ["lastNotNull"], "fields": ""}}
    return p


def count_chart(title, x, y, expr, label, description, color):
    p = panel("barchart", title, x, y, 12, 8, description=description)
    p["targets"] = [target(expr, instant=True, table=True)]
    p["transformations"] = [{"id": "organize", "options": {"excludeByName": {"Time": True}, "renameByName": {"Value": "Estimated probes"}}}]
    p["fieldConfig"]["defaults"].update({"decimals": 0, "min": 0, "color": {"mode": "fixed", "fixedColor": color},
        "custom": {"axisPlacement": "auto", "axisColorMode": "text", "axisLabel": "Probes (estimated)",
                   "hideFrom": {"legend": False, "tooltip": False, "viz": False}, "scaleDistribution": {"type": "linear"}}})
    p["options"] = {"xField": label, "orientation": "horizontal", "barWidth": 0.75, "groupWidth": 0.8,
                    "barRadius": 0.1, "showValue": "always", "stacking": "none", "xTickLabelMaxLength": 50,
                    "legend": {"showLegend": False, "displayMode": "list", "placement": "bottom"},
                    "tooltip": {"mode": "single", "sort": "none"}}
    return p


panels.append(text("", 0, 3, "### PAYMENT PROVIDER OPERATIONS\nLive HTTP health · exact status rules · select an instance or provider to investigate. **Gray / orange = unknown; not healthy.**"))
stat("Scrape health", 0, f"min({UP})", "وضعیت ارتباط Prometheus با exporterهای انتخاب‌شده؛ این کارت سلامت سرویس پرداخت نیست. اگر یکی از targetها scrape نشود، FAILED نمایش داده می‌شود.", maps=mapping({0: ("FAILED", RED), 1: ("REACHABLE", GREEN)}, "NO TARGET DATA"))
stat("Healthy providers", 4, f"sum({STATE} == bool 0)", "تعداد سرویس‌های دارای نتیجهٔ تازه و سالم طبق کدهای مجاز تنظیم‌شده. دادهٔ قدیمی یا exporter قطع‌شده سالم محسوب نمی‌شود.", color=GREEN)
stat("Unhealthy providers", 8, f"sum({STATE} == bool 3)", "تعداد probeهای تازه و ناموفق؛ شامل کد نامجاز، timeout و خطای بدنهٔ پاسخ.", limits=thresholds(GREEN, (1, RED)))
stat("Unknown / stale", 12, f"sum(({STATE} > bool 0) * ({STATE} < bool 3))", "سرویس‌هایی که در بازهٔ انتخابی دیده شده‌اند اما اکنون نتیجهٔ قابل اعتماد ندارند: scrape قطع، دادهٔ قدیمی، تغییر config یا اختلاف ساعت بیش از ۵ ثانیه در آینده.", limits=thresholds(GREEN, (1, AMBER)))
total_probes = f"sum(increase({COUNT}[$__range]))"
total_errors = f"sum(increase({ERRORS}[$__range]))"
stat("Probe success · ${{__range}}".replace("${{", "${").replace("}}", "}"), 16,
     f"(clamp_max(clamp_min(1 - {total_errors} / {total_probes}, 0), 1) and ({total_probes} > 0))",
     "نسبت تخمینی probeهای موفق به probeهای مشاهده‌شده در بازهٔ انتخابی؛ SLA یا درصد موفقیت تراکنش نیست. زمان‌های بدون scrape از این نسبت قابل استنتاج نیستند. بدون probe کافی مقدار NO DATA نمایش داده می‌شود.", unit="percentunit", limits=thresholds(RED, (0.95, AMBER), (0.99, GREEN)))
stat("p95 latency · recent", 20, P95, "صدک ۹۵ تخمینی همهٔ probeهای انتخاب‌شده در پنجرهٔ $__rate_interval؛ شامل خطاها و زمان خواندن کامل بدنه. آبی صرفاً مقدار اندازه‌گیری‌شده است، نه تأیید SLA.", unit="s")

table = panel("table", "Provider status · failures first", 0, 7, 24, 12,
              description="وضعیت اکنون با دادهٔ تازه؛ موارد مشکل‌دار بالاتر هستند. HTTP 401/404 ممکن است طبق config سالم باشند، بنابراین رنگ سلامت از State می‌آید. Probe success مربوط به بازهٔ انتخابی است. روی نام Provider کلیک کنید تا همان سرویس و instance فیلتر شود.")
historic_stamp = grouped(f"last_over_time({metric('payment_provider_last_probe_timestamp_seconds')}[$__range])")
table_exprs = [STATE,
    f"({grouped(metric('payment_provider_http_status_code'))} {FRESH_FILTER})",
    DURATION, BUDGET, WINDOW_SUCCESS,
    f"time() - {historic_stamp}"]
table["targets"] = [target(expr, ref=chr(65 + i), instant=True, table=True) for i, expr in enumerate(table_exprs)]
renames = {"provider": "Provider", "instance": "Instance", "Value #A": "State", "Value #B": "HTTP code", "Value #C": "Last latency", "Value #D": "Deadline used", "Value #E": "Probe success", "Value #F": "Last completion age"}
table["transformations"] = [{"id": "merge", "options": {}}, {"id": "organize", "options": {
    "excludeByName": {"Time": True, "job": True, "__name__": True},
    "indexByName": {name: i for i, name in enumerate(renames)}, "renameByName": renames}}]
table["options"] = {"showHeader": True, "cellHeight": "sm", "sortBy": [{"displayName": "State", "desc": True}],
                    "footer": {"show": False, "reducer": [], "countRows": False}}
table["fieldConfig"]["defaults"].update({"color": {"mode": "fixed", "fixedColor": BLUE}, "custom": {"align": "auto", "cellOptions": {"type": "auto"}, "inspect": False}})


def override(name, **properties):
    return {"matcher": {"id": "byName", "options": name}, "properties": [{"id": key, "value": value} for key, value in properties.items()]}


table["fieldConfig"]["overrides"] = [
    override("Provider", **{"custom.width": 180, "links": [{"title": "Focus this provider & instance", "targetBlank": False,
        "url": "/d/${__dashboard.uid}?var-datasource=${datasource:percentencode}&var-job=${job:percentencode}&var-instance=${__data.fields.Instance:percentencode}&var-provider=${__data.fields.Provider:percentencode}&var-freshness=${freshness}&from=${__from}&to=${__to}"}]}),
    override("Instance", **{"custom.width": 190}),
    override("State", mappings=mapping({0: ("HEALTHY", GREEN), 1: ("STALE / MISSING", GRAY), 2: ("SCRAPE FAILED", AMBER), 3: ("UNHEALTHY", RED)}, "UNKNOWN"),
             **{"custom.cellOptions": {"type": "color-background", "mode": "basic"}, "custom.width": 170, "decimals": 0}),
    override("HTTP code", unit="short", decimals=0, mappings=mapping({0: ("NO RESPONSE", GRAY)}, None), **{"custom.width": 110}),
    override("Last latency", unit="s", decimals=2),
    override("Deadline used", unit="percentunit", decimals=1, min=0, max=1,
             thresholds=thresholds(BLUE, (0.8, AMBER), (1, RED)), color={"mode": "thresholds"},
             **{"custom.cellOptions": {"type": "color-text"}}),
    override("Probe success", unit="percentunit", decimals=2, min=0, max=1),
    override("Last completion age", unit="s", decimals=0),
]
panels.append(table)
panels.append(row("01 / AVAILABILITY & RESPONSE TIME", 19))
panels.append(timeline("HTTP health history · gaps mean unknown", 20, 9, HTTP,
    "سبز: probe موفق طبق config. قرمز: probe ناموفق. گپ: نتیجهٔ تازه و قابل اعتماد وجود ندارد؛ گپ به معنی سالم بودن نیست. برای بررسی مشکل scrape، بخش Exporter diagnostics را باز کنید.", {0: ("UNHEALTHY", RED), 1: ("HEALTHY", GREEN)}))
panels.append(series("p95 probe latency", 0, 29, 12,
    f"histogram_quantile(0.95, sum by ({KEY}, le) (rate({metric('payment_provider_latency_seconds_bucket')}[$__rate_interval]))) and {FRESH}",
    "صدک ۹۵ برای هر provider و instance؛ از bucketهای histogram تخمین زده می‌شود و شامل probeهای ناموفق هم هست. در نبود scrape معتبر، نمودار گپ دارد."))
panels.append(series("Probe success · recent window", 12, 29, 12, RECENT_SUCCESS,
    "نسبت موفقیت probeهای مشاهده‌شده در پنجرهٔ $__rate_interval، نه تراکنش‌ها یا SLA. اگر تلاش کافی یا scrape معتبر نباشد نمودار گپ دارد.", "percentunit"))
panels.append(bars("Current latency · slowest first", 0, 37, f"sort_desc({DURATION})", "زمان کامل آخرین probe تازه، شامل خواندن بدنهٔ پاسخ. سرویس‌های کندتر بالاتر قرار می‌گیرند."))
panels.append(bars("HTTP deadline used", 12, 37, f"sort_desc({BUDGET})", "نسبت زمان probe به timeout همان سرویس. ۸۰٪ به بالا هشدار نزدیک شدن به سقف و ۱۰۰٪ به بالا رسیدن به سقف است. نتیجهٔ واقعی موفقیت را از State بخوانید.", "percentunit", True))
panels.append(row("02 / FAILURE INVESTIGATION", 44))
panels.append(count_chart("Failure causes · ${{__range}}".replace("${{", "${").replace("}}", "}"), 0, 45,
    f"sort_desc(sum by (reason) (increase({ERRORS}[$__range])))", "reason",
    "افزایش تخمینی شمارندهٔ خطاها در بازهٔ انتخابی؛ صفر یعنی خطایی از این دسته مشاهده نشده. status=کد نامجاز، body=پاسخ ناقص، body_too_large=حجم بیش از حد، timeout=پایان مهلت.", RED))
panels.append(count_chart("HTTP responses · ${{__range}}".replace("${{", "${").replace("}}", "}"), 12, 45,
    f"sort_desc(sum by (code) (increase({metric('payment_provider_request_total')}[$__range])))", "code",
    "توزیع تخمینی کد پاسخ probeها. کد ۰ یعنی پاسخ قابل استفاده دریافت نشده. ۲۰۰ هم می‌تواند با خطای خواندن بدنه ناموفق باشد؛ ۴۰۱ یا ۴۰۴ هم می‌تواند طبق config سالم باشد. این نمودار به‌تنهایی سلامت را تعیین نمی‌کند.", BLUE))
panels.append(series("Failure rate by cause", 0, 53, 12,
    f"sum by (reason) (rate({ERRORS}[$__rate_interval])) * 60",
    "نرخ خطاهای probe در دقیقه به تفکیک علت؛ برای تشخیص تغییر الگوی timeout، TLS، شبکه یا کد پاسخ.", "suffix: /min", legend="{{reason}}"))
panels.append(series("Probe rate", 12, 53, 12, f"{RATE_PROBES} * 60",
    "تعداد probe در دقیقه برای هر سرویس؛ این اعداد درخواست‌های مانیتورینگ هستند و حجم تراکنش پرداخت را نشان نمی‌دهند.", "suffix: /min"))

mtr_success = f"({grouped(metric('payment_provider_mtr_success'))} {FRESH_FILTER})"
mtr_children = [text("MTR · optional network diagnostics", 62, 3,
    "**No MTR data is expected when disabled.** MTR success measures whether the destination could be measured; it is independent of HTTP health. Loss and RTT gaps indicate missing/failed measurements, not zero loss. TCP checks use the URL port; ICMP may be filtered.")]
mtr_children.append(timeline("MTR measurement history", 65, 7, mtr_success,
    "سبز: گزارش معتبر برای IP مقصد. قرمز: اندازه‌گیری شکست خورده یا مقصد در گزارش پیدا نشده. گپ: MTR خاموش است یا دادهٔ تازه وجود ندارد. وضعیت MTR سلامت HTTP را تغییر نمی‌دهد.", {0: ("MEASUREMENT FAILED", RED), 1: ("MEASURED", GREEN)}))
for metric_name, title, x, unit in [("payment_provider_mtr_packet_loss_ratio", "Destination packet loss", 0, "percentunit"), ("payment_provider_mtr_avg_latency_seconds", "Destination average RTT", 12, "s")]:
    expression = f"{grouped(metric(metric_name))} and on ({KEY}) ({mtr_success} == 1)"
    mtr_children.append(series(title, x, 72, 12, expression,
        "فقط اندازه‌گیری معتبر مقصد نمایش داده می‌شود؛ خروجی نامعتبر یا خاموش بودن MTR صفر فرض نمی‌شود. RTT میانگین واقعی Avg است. برای میزبان load-balanced ممکن است IP اندازه‌گیری‌شده با اتصال HTTP متفاوت باشد.", unit,
        limits=thresholds(GREEN, (0.01, AMBER), (0.05, RED)) if unit == "percentunit" else None))
panels.append(row("03 / NETWORK DIAGNOSTICS · MTR OPTIONAL", 61, True, mtr_children))

runtime_children = []
runtime_state = timeline("Exporter scrape history", 63, 7, UP, "این تاریخچه وضعیت scrape خود exporter را نشان می‌دهد. قرمز با خرابی قطعی provider برابر نیست؛ در این وضعیت نتیجهٔ سرویس‌ها نامشخص است.", {0: ("SCRAPE FAILED", RED), 1: ("SCRAPING", GREEN)})
runtime_state["gridPos"]["w"] = 12
runtime_state["targets"][0]["legendFormat"] = "{{instance}}"
runtime_children.append(runtime_state)
runtime_children.append(series("Prometheus scrape duration", 12, 63, 12, metric("scrape_duration_seconds", False), "مدت scrape از دید Prometheus؛ شامل کل batch probeها و MTR فعال است. این مقدار را با scrape_timeout تنظیم‌شده مقایسه کنید.", legend="{{instance}}"))
runtime_children.extend([
    series("Exporter CPU · one core = 100%", 0, 71, 8, f"rate({metric('process_cpu_seconds_total', False)}[$__rate_interval])", "CPU مصرف‌شده توسط فرایند exporter؛ ۱۰۰٪ یعنی یک هستهٔ کامل، نه کل ظرفیت ماشین.", "percentunit", legend="{{instance}}"),
    series("Exporter resident memory", 8, 71, 8, metric("process_resident_memory_bytes", False), "حافظهٔ resident فرایند exporter؛ حافظهٔ VM یا کل کانتینر نیست.", "bytes", legend="{{instance}}"),
    series("Go goroutines", 16, 71, 8, metric("go_goroutines", False), "تعداد goroutineهای فرایند exporter؛ روند رو به رشد مداوم می‌تواند برای بررسی مصرف منابع مفید باشد.", "short", legend="{{instance}}"),
])
# A multicore Go process may use more than one CPU core.
runtime_children[2]["fieldConfig"]["defaults"].pop("max", None)
build = panel("table", "Exporter build inventory", 0, 79, 24, 6, description="نسخه و اطلاعات build هر exporter انتخاب‌شده؛ metadata صرفاً متعلق به exporter است.")
build["targets"] = [target(f"max by (job, instance, version, revision, goversion) ({metric('payment_exporter_build_info', False)})", instant=True, table=True)]
build["options"] = {"showHeader": True, "cellHeight": "sm", "sortBy": [{"displayName": "Instance", "desc": False}]}
build["transformations"] = [{"id": "organize", "options": {"excludeByName": {"Time": True, "Value": True, "__name__": True},
    "indexByName": {"instance": 0, "job": 1, "version": 2, "goversion": 3, "revision": 4},
    "renameByName": {"instance": "Instance", "job": "Job", "version": "Exporter version", "goversion": "Go version", "revision": "Revision"}}}]
runtime_children.append(build)
panels.append(row("04 / EXPORTER DIAGNOSTICS", 62, True, runtime_children))
panels.append(row("05 / HOW TO READ THIS DASHBOARD", 63, True, [text("Operational guide / راهنمای خواندن", 64, 11,
    "### Read the current state first\n"
    "- **HEALTHY**: a fresh probe completed and returned one of the provider's configured status codes.\n"
    "- **UNHEALTHY**: a fresh probe failed: unexpected status, timeout, TLS/network error, truncated or oversized body.\n"
    "- **SCRAPE FAILED**: Prometheus cannot collect from the exporter. The provider state is unknown.\n"
    "- **STALE / MISSING**: no trustworthy current result for a provider observed in the selected time range. This may include removed configuration entries.\n\n"
    "### Interpret the numbers correctly\n"
    "**Probe success** describes observed monitoring attempts, not transactions, business success, full uptime or an SLA. Missing scrapes are unknown. `increase()` extrapolates, so window counts are estimates. p95 is an estimate from histogram buckets and includes failed probes. HTTP 401/404 may be healthy; HTTP 200 with a body timeout is unhealthy. MTR is optional and independent.\n\n"
    "### Filters & investigation\n"
    "Select the Prometheus datasource, exporter job, instance and provider. Click a provider name in the status table to focus that provider and instance. **Freshness limit** defaults to 180 seconds; increase it for intentionally slow scrape intervals. A completion timestamp more than 5 seconds in the future is treated as untrustworthy. Jobs/providers are discovered from metrics; the exporter must have been scraped successfully at least once in the selected period.\n\n"
    "**راهنمای کوتاه:** ابتدا State و Scrape health را بخوانید. دادهٔ قدیمی یا گپ به معنی سالم بودن نیست. سپس deadline، کد پاسخ و علت خطا را بررسی کنید. بخش‌های MTR و منابع exporter پیش‌فرض جمع شده‌اند؛ برای جزئیات بازشان کنید. Refresh فقط Prometheus را query می‌کند و مستقیماً probe جدید ایجاد نمی‌کند.")]))


def query_variable(name, label, query, current, multi=False, all_values=False):
    variable = {"name": name, "label": label, "type": "query", "datasource": copy.deepcopy(DS),
            "query": {"query": query, "refId": "VariableQuery"}, "definition": query, "refresh": 2,
            "sort": 1, "multi": multi, "includeAll": all_values,
            "options": [], "current": current, "hide": 0, "skipUrlSync": False}
    if all_values:
        variable["allValue"] = ".*"
    return variable


dashboard = {
    "__inputs": [{"name": "DS_PROMETHEUS", "label": "Prometheus", "description": "Prometheus datasource that scrapes Payment Exporter 1.0.0", "type": "datasource", "pluginId": "prometheus", "pluginName": "Prometheus"}],
    "__requires": [{"type": "grafana", "id": "grafana", "name": "Grafana", "version": "10.4.0"},
        {"type": "datasource", "id": "prometheus", "name": "Prometheus", "version": "1.0.0"}] +
        [{"type": "panel", "id": kind, "name": kind, "version": ""} for kind in ["stat", "table", "timeseries", "state-timeline", "bargauge", "barchart", "text"]],
    "id": None, "uid": "paystar-payment-exporter", "title": "Payment Exporter / Provider Operations",
    "description": "Payment Exporter 1.0.0 operations: freshness-aware HTTP state, observed probe success, latency, failure diagnostics, optional MTR and exporter health. Built-in panels only; no extra plugins or recording rules. HTTP status codes are not automatically treated as healthy/unhealthy.",
    "tags": ["paystar", "payment-exporter", "prometheus", "http", "operations"],
    "timezone": "browser", "editable": True, "graphTooltip": 1, "schemaVersion": 36, "version": 1,
    "refresh": "30s", "time": {"from": "now-6h", "to": "now"},
    "timepicker": {"refresh_intervals": ["30s", "1m", "2m", "5m", "15m", "30m", "1h"]},
    "annotations": {"list": [{"builtIn": 1, "datasource": {"type": "grafana", "uid": "-- Grafana --"}, "enable": True, "hide": True, "iconColor": "rgba(86, 211, 201, 1)", "name": "Annotations & Alerts", "type": "dashboard"}]},
    "links": [], "panels": panels,
    "templating": {"list": [
        {"name": "datasource", "label": "Prometheus", "type": "datasource", "query": "prometheus", "refresh": 1, "hide": 0, "multi": False, "includeAll": False, "options": [], "current": {"text": "Prometheus", "value": "${DS_PROMETHEUS}"}},
        query_variable("job", "Exporter job", "label_values(payment_exporter_build_info, job)", {"text": "payment-exporter", "value": "payment-exporter"}),
        query_variable("instance", "Instance", 'label_values(up{job=~"${job:regex}"}, instance)', {"text": "All", "value": "$__all"}, True, True),
        query_variable("provider", "Provider", 'label_values(payment_provider_up{job=~"${job:regex}",instance=~"${instance:regex}"}, provider)', {"text": "All", "value": "$__all"}, True, True),
        {"name": "freshness", "label": "Freshness limit (s)", "type": "custom", "query": "60,120,180,300,600", "hide": 0, "multi": False, "includeAll": False,
         "current": {"text": "180", "value": "180"}, "options": [{"text": str(v), "value": str(v), "selected": v == 180} for v in [60, 120, 180, 300, 600]]},
    ]},
}

if __name__ == "__main__":
    output = ROOT / "grafana/payment-exporter.dashboard.json"
    output.write_text(json.dumps(dashboard, ensure_ascii=False, indent=2) + "\n")
    destination = ROOT / "dist/payment-exporter-grafana-dashboard.json"
    destination.parent.mkdir(exist_ok=True)
    destination.write_bytes(output.read_bytes())
    print(destination)
