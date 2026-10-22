# Example configs

Ready-to-use example configurations for proxy-socks5-balancer:

- `simple.yaml` — minimal two equal upstreams
- `failover-tiers.yaml` — priority tiers primary/backup
- `weighted.yaml` — prefer a faster line with weight
- `flaky-links.yaml` — hysteresis + latency smoothing for unstable links
- `monitoring.yaml` — JSON logs, on_switch hook, exposed metrics port
- `prometheus-scrape.yml` — Prometheus scrape snippet

Run one:

```
./balancer -config examples/simple.yaml
```

---

# Примеры конфигураций

Готовые примеры конфигураций для proxy-socks5-balancer:

- `simple.yaml` — минимальный набор из двух равноправных апстримов
- `failover-tiers.yaml` — уровни приоритета основной/резервный
- `weighted.yaml` — предпочтение более быстрой линии через weight
- `flaky-links.yaml` — гистерезис и сглаживание задержки для нестабильных каналов
- `monitoring.yaml` — JSON-логи, хук on_switch, открытый порт метрик
- `prometheus-scrape.yml` — фрагмент конфигурации Prometheus для сбора метрик

Запуск одного из примеров:

```
./balancer -config examples/simple.yaml
```
