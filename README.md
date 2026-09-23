# Modular Go Backend

Starter backend Go dengan PostgreSQL terpisah dari template Express. Kontrak HTTP berada di registry `internal/httpapi/registry.go`, sedangkan rencana dan kriteria penerimaan ada di [IMPLEMENTATION-PLAN.md](IMPLEMENTATION-PLAN.md).

Snapshot method, path, status, akses, serta policy dari registry Express ada di `contracts/express-endpoints.json` dan dibandingkan dengan registry Go pada setiap `go test`. Perbarui fixture hanya setelah perubahan kontrak pada kedua template disepakati.

## Mulai manual

Perlu Go 1.27.1 dan PostgreSQL 18. Salin `.env.example` menjadi `.env`, lalu ganti `JWT_SECRET`, kredensial database, dan password bootstrap. Buat database kosong yang cocok dengan `DATABASE_URL`.

```sh
go run ./cmd/migrate
go run ./cmd/seed
go run ./cmd/api
```

Seed adalah langkah eksplisit. Aplikasi tidak menjalankan migrasi atau seed saat startup. Endpoint `/live` membuktikan HTTP hidup; `/ready` memeriksa database dan Redis bila dipakai untuk rate limit. Dokumentasi otomatis tersedia di `/docs` dan `/docs/openapi.json`.

Validasi lokal tanpa layanan eksternal: `go test ./...`, `go vet ./...`, atau `pwsh -File scripts/verify-template.ps1`. Tes PostgreSQL dan Redis otomatis berjalan bila `DATABASE_URL` atau `REDIS_URL` disediakan. Ubah query SQL di `internal/db/queries` dan jalankan `make sqlc` untuk memperbarui kode query bertipe.

## Mulai lewat Compose

```sh
docker compose up --build -d
docker compose run --rm --entrypoint seed app
```

Service `migrate` selesai sebelum `app` dimulai. Redis dan MinIO tersedia dengan `--profile redis` dan `--profile minio`; initializer mengisi `COMPOSE_PROFILES` sesuai pilihan. Service `minio-init` membuat bucket development. Untuk S3 production, buat bucket melalui proses provisioning infrastruktur. Port host bisa diubah lewat `APP_PORT` atau file `compose.override.yaml` yang disalin dari contoh. Untuk deployment replica di luar Compose, jalankan `migrate` sebagai job rilis tunggal sebelum menjalankan image `api` baru.

Profile MinIO dimaksudkan untuk pengembangan lokal. Untuk production, arahkan adapter S3 ke layanan object storage yang aktif dipelihara; [repositori MinIO community telah diarsipkan](https://github.com/minio/minio).

## Konfigurasi

- `ENDPOINT_POLICIES_JSON` mengubah `audit`, `rateLimit`, atau `cache` untuk ID endpoint yang diketahui. Contoh: `{"user.get":{"audit":"optional","cache":"off"}}`. Validasi startup menolak ID dan nilai yang tidak dikenal. Perubahan env membutuhkan redeploy.
- Rate group `auth`, `public`, `internal` memakai `RATE_LIMIT_<GROUP>_WINDOW_MS` dan `RATE_LIMIT_<GROUP>_MAX`. Satu instance bisa memakai memory; lebih dari satu instance memerlukan Redis.
- `CACHE_ENABLED=false` berarti Redis tidak dipakai untuk cache. Jika hanya cache yang memakai Redis, kegagalannya tidak menggagalkan readiness.
- `UPLOAD_STORAGE=local|s3`; file lokal masuk `UPLOAD_LOCAL_DIR`. Jalankan `go run ./cmd/cleanup-uploads` untuk melihat orphan dan tambah `--apply` untuk menghapusnya.
- Semua instant dikirim sebagai UTC ISO 8601; gunakan zona IANA seperti `Asia/Jakarta` di sisi penyajian pengguna. Database menyimpan `timestamptz`.
- Production mewajibkan `CORS_ORIGINS` eksplisit. Permintaan tanpa Origin tetap bisa diproses.
- `OTEL_ENABLED=true` mengirim trace dan metrik HTTP melalui OTLP HTTP ke `OTEL_EXPORTER_OTLP_ENDPOINT`; set `OTEL_SERVICE_NAME` untuk identitas aplikasi.

Untuk backup, gunakan `pg_dump` terhadap database Go dan simpan objek upload secara terpisah. Lakukan uji restore ke staging sebelum rilis production.

## Buat proyek baru

Jalankan `go run ./cmd/initproject ../my-api` dari checkout template. Wizard meminta module path, port, database, pilihan Redis dan storage. Tambahkan `--no-install` sebelum nama folder untuk melewati `go mod download`. Tujuan yang sudah berisi file ditolak; initializer menyalin source tanpa direktori Git, data upload, atau `.env` asal, lalu membuat `.env` baru dengan JWT secret acak.
