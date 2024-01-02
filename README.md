# Geo API benchmark

```bash
go install github.com/ringsaturn/geo-api-benchmark@latest
```

```bash
geo-api-benchmark -api "http://localhost:8000/?lon=%.4f&lat=%.4f" -coords lon,lat -qps 2000 -threads 100 -runs 1000 -timeout 1 -country JP
```
