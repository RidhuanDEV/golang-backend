# Rencana implementasi modular Go backend

## 1. Tujuan dan batas kontrak

Bangun starter backend Go pada repo mandiri `github.com/RidhuanDEV/golang-backend`, di folder `template-JS/modular-golang` yang sejajar dengan repo Express. PostgreSQL tetap menjadi database utama. Implementasi Go memakai database PostgreSQL **tersendiri** dan migrasi Go sendiri; tidak menjalankan Goose pada database yang dikelola Prisma. Repo Express adalah acuan perilaku API saat rencana ini ditulis, bukan dependensi runtime atau tempat mengubah kode.

Hasil akhir harus dapat digunakan untuk memulai proyek baru secara manual atau melalui Docker, dengan endpoint Auth, User, Role, Permission, Upload, sistem kesehatan, dan docs yang setara. Kesetaraan berarti metode/path, status, bentuk JSON, aturan akses, audit, rate limit, dan perilaku opsional Redis/storage dapat diuji terhadap kontrak Express. Struktur internal Go mengikuti idiom Go dan tidak menyalin layer TypeScript secara mekanis.

Repo remote masih kosong saat perencanaan. Rencana ini adalah artefak pertama; implementasi, commit, dan push dilakukan sebagai langkah berikutnya.

## 2. Stack dan keputusan arsitektur

| Kebutuhan | Pilihan | Keputusan pemakaian |
| --- | --- | --- |
| Bahasa/runtime | Go 1.27.1 | Gunakan rilis stabil dan pin toolchain/image pada patch yang diuji; `go.mod` menyatakan `go 1.27`. Mesin lokal saat ini Go 1.26.3, sehingga implementasi memerlukan toolchain 1.27. |
| HTTP | `net/http` + `go-chi/chi/v5` | Router dan middleware standar `http.Handler`; satu `http.Server` dengan timeout, batas body, dan graceful shutdown. |
| DTO, validasi, API docs | `huma/v2` dengan adapter `humachi` | Struct input/output Go menghasilkan validasi dan OpenAPI 3.1. Semua operasi didaftarkan melalui wrapper registry; artefak OpenAPI dibuat dari registrasi yang sama, tanpa komentar Swagger/JSDoc. |
| PostgreSQL | `jackc/pgx/v5` + `pgxpool` | Koneksi utama, transaksi, ping/readiness, dan timeout berbasis `context.Context`. |
| Query bertipe | `sqlc` dengan target `pgx/v5` | Query SQL eksplisit; kode query hasil generate di-commit dan diperiksa ulang di CI. Tidak memakai ORM reflektif. |
| Migrasi | `pressly/goose/v3` | File SQL versioned, satu job migrasi sebelum aplikasi mulai; migrasi tidak berjalan di tiap replica. |
| Auth | `golang-jwt/jwt/v5` + `golang.org/x/crypto/bcrypt` | Token bearer bertipe dengan verifikasi algoritma yang eksplisit; hash password kompatibel dengan pola bcrypt template Express. |
| Cache/rate store | `redis/go-redis/v9` | Redis opsional untuk cache dan wajib untuk rate limit bila `APP_INSTANCE_COUNT > 1`; mode tanpa Redis tetap didukung. |
| Upload | `net/http` multipart + AWS SDK for Go v2 S3 | Adapter lokal dan S3 kompatibel MinIO; metadata PostgreSQL dan cleanup orphan. |
| Logging/telemetri | `log/slog` JSON + OpenTelemetry Go | Log terstruktur standar library; trace dan metrics dapat diaktifkan lewat env. Rahasia, token, query sensitif, dan byte file tidak dicatat. |
| Keamanan dependensi | `govulncheck` | Gate CI bersama `go vet`, race test, dan audit dependency setelah versi dikunci di `go.mod`/`go.sum`. |

Rujukan utama: [Go 1.27](https://go.dev/blog/go1.27), [Chi](https://github.com/go-chi/chi), [Huma + Chi](https://huma.rocks/features/bring-your-own-router/), [pgx](https://github.com/jackc/pgx), [sqlc](https://docs.sqlc.dev/en/latest/guides/using-go-and-pgx.html), [Goose](https://pressly.github.io/goose/), [go-redis](https://github.com/redis/go-redis), [AWS SDK Go v2](https://github.com/aws/aws-sdk-go-v2), dan [govulncheck](https://go.dev/doc/security/vuln/). Pin versi library stabil yang teruji ketika implementasi dimulai; jangan memakai `@latest` pada build CI/production.

### Susunan repo

```text
cmd/api/                 # HTTP server
cmd/migrate/             # migrasi satu kali; embed SQL migration
cmd/seed/                # role, permission, bootstrap user eksplisit
cmd/cleanup-uploads/     # daftar orphan default, --apply untuk hapus
cmd/initproject/         # inisialisasi salinan template/proyek baru
internal/config/         # parse dan validasi env
internal/httpapi/        # Huma/Chi, registry, middleware, response, docs
internal/auth/           # JWT, password, autentikasi, RBAC
internal/audit/          # audit mode dan snapshot aman
internal/cache/          # cache typed dan invalidasi
internal/ratelimit/      # memory/Redis fixed-window
internal/storage/        # local/S3 upload adapters
internal/modules/        # auth, users, roles, permissions, uploads
internal/db/migrations/  # SQL Goose; satu sumber schema
internal/db/queries/     # SQL sqlc per modul
internal/db/sqlc/        # kode hasil generate
internal/timeutil/       # parsing instant dan zona IANA
contracts/              # matriks parity, golden request/response
scripts/                # verifikasi dan tooling lintas platform bila perlu
.github/workflows/      # CI, build image, pemeriksaan security
```

`cmd/initproject` meminta nama proyek dan module path Go, port, nama/kredensial database, opsi Redis, serta local/S3. Ia membuat `.env` dengan secret acak, menyesuaikan identitas proyek tanpa mengubah source template, dan menolak folder tujuan yang tidak kosong. Tidak ada auto-seed di startup aplikasi.

## 3. Kontrak API dan data

### Endpoint yang harus terdaftar

| Modul | Operasi |
| --- | --- |
| System/docs | `GET /health`, `/live`, `/ready`, `/docs`, `/docs/openapi.json`, `/docs/specs/{module}.json` |
| Auth | `POST /api/auth/register`, `POST /api/auth/login`, `GET /api/auth/me` |
| Users | `GET /api/users`, `GET /api/users/{id}`, `POST /api/users`, `PATCH /api/users/{id}`, `DELETE /api/users/{id}` |
| Roles | `GET /api/roles`, `GET /api/roles/{id}`, `POST /api/roles`, `PATCH /api/roles/{id}`, `DELETE /api/roles/{id}`, `POST /api/roles/{id}/permissions` |
| Permissions | `GET /api/permissions`, `GET /api/permissions/{id}`, `POST /api/permissions`, `PATCH /api/permissions/{id}`, `DELETE /api/permissions/{id}` |
| Upload | `POST /api/upload` dengan field multipart `file`, `GET /api/upload/{id}` untuk metadata saja |

Kontrak sukses: `{"success":true,"data":...}` dengan `meta` pagination bila ada; create `201`, delete `204` tanpa body. Kontrak gagal: `{"success":false,"message":"...","errors":[]}` dan status HTTP yang sesuai. Auth bearer token berlaku 24 jam, payload publik berisi `id`, `email`, dan `roleId`; password/hash tidak pernah keluar. Endpoint internal tetap membaca status user dan grant RBAC dari PostgreSQL agar perubahan hak akses tidak menunggu expiry cache. Query list user mempertahankan `page`, `limit`, `sortBy`, `orderBy`, `search`, dan `fields` yang di-allowlist. Simpan contoh request/response/status dari repo Express sebagai golden fixtures; bandingkan semantik OpenAPI dan respons Go dengan fixtures tersebut di CI.

### Schema PostgreSQL milik Go

Gunakan tabel `users`, `roles`, `permissions`, `role_permissions`, `activity_logs`, dan `stored_files` dalam database Go sendiri. PK/FK UUID, unique email/nama, FK dan aturan penghapusan eksplisit, soft delete user, index query list/audit/upload, serta `timestamptz(3)` untuk setiap instant. `activity_logs` menyimpan behavior, module, entity ID, user FK nullable, actor ID snapshot, before/after JSONB, request ID, endpoint ID, dan created_at. `stored_files` menyimpan storage, status, object key unik, nama asli, MIME, ukuran, uploader FK nullable, dan created_at. SQL migrasi menjadi sumber schema untuk `sqlc`; tidak memakai tabel Prisma secara langsung dan tidak mengonversi data lama secara implisit.

API mengirim waktu ISO 8601 UTC. Input instant harus menyertakan offset/Z; utilitas memakai `time.Parse(time.RFC3339Nano)` dan `time.LoadLocation` untuk WIB/WITA/WIT serta zona luar negeri. Zona user hanya dipakai pada batas tampilan; host server tidak menentukan jam bisnis. Koneksi PostgreSQL menggunakan sesi UTC. Tanggal tanpa waktu tetap diperlakukan sebagai tipe bisnis terpisah dari instant.

## 4. Mekanisme inti

1. **Registry terpusat.** Definisikan `EndpointID` sebagai konstanta bertipe dan `EndpointDefinition` berisi method/path/module, akses, audit `required|optional|none`, rate group `auth|public|internal`, cache `read|off`, dan metadata operasi. Wrapper registrasi memasangkan definisi dengan handler Huma tepat satu kali; startup/CI gagal bila ID tidak terpasang, duplikat, atau method/path bertabrakan. `ENDPOINT_POLICIES_JSON` hanya boleh mengubah audit/rate/cache untuk ID dikenal saat startup; perubahan env berlaku setelah redeploy. Operasi docs memakai DTO Go yang sama dengan validasi dan respons.
2. **Auth dan RBAC.** Register/login memakai bcrypt; verifikasi JWT membatasi algoritma dan expiry, lalu membaca user aktif/role/permission dari database. Middleware urut: request ID → recovery/logging/CORS → public rate → auth → permission → internal rate → validasi → handler. Error 401/403/404/409 tetap terpisah. `CORS_ORIGINS` wajib di production, origin browser di-allowlist, dan request server tanpa Origin tetap diterima. Trust proxy hanya sesuai jumlah proxy tepercaya.
3. **Audit.** Mutasi `required` menulis perubahan dan audit dalam `pgx.Tx` yang sama melalui query `sqlc.WithTx`; tanpa audit transaksi tidak boleh commit. `optional` menulis best effort setelah commit dan mencatat kegagalannya; `none` tidak menulis. GET yang diubah menjadi optional mencatat READ metadata saja. Snapshot dibuat dengan projection allowlist per service dan redaksi rekursif sebagai lapisan kedua. Endpoint registry dapat mengubah kebijakan saat deploy tanpa UI admin runtime.
4. **Rate limit.** Pertahankan tiga group dan nama env `RATE_LIMIT_{AUTH,PUBLIC,INTERNAL}_{WINDOW_MS,MAX}`. Implementasikan fixed-window atomic: map bermutex dengan TTL untuk satu instance, Lua `INCR`+`EXPIRE` untuk Redis multi-instance. Kunci public/auth berdasarkan IP yang sudah melalui trust-proxy config; internal berdasarkan user ID. Auth fail closed saat Redis rate store gagal, public/internal fail open sambil logging; `/health`, `/live`, `/ready` tidak menghabiskan kuota. Konfigurasi multi-instance dengan memory ditolak saat startup.
5. **Cache.** `CACHE_ENABLED=false` tidak membuat koneksi Redis. Saat aktif, hanya endpoint berlabel `read` yang menyimpan DTO JSON bertipe dengan TTL dan prefix versi; cache rusak/miss/gagal jatuh ke PostgreSQL. Invalidasi setelah commit. Auth/RBAC tidak memakai cache. Contoh nyata: user read/list dan upload metadata. `/ready` menguji Redis hanya bila Redis wajib untuk rate limit.
6. **Upload.** `UPLOAD_STORAGE=local|s3`, `UPLOAD_ENABLED`, batas ukuran, MIME allowlist, signature file, random object key, dan path lokal yang dibatasi ke root uploads. S3 AWS SDK v2 mendukung endpoint MinIO/path-style via env. Simpan object dahulu, lalu metadata plus audit; bila DB gagal, hapus object best effort. Sediakan cleanup orphan yang default dry run dan baru menghapus dengan `--apply`; tidak membuka endpoint download publik sebagai default.
7. **Waktu, log, dan observability.** `slog` JSON mencatat request ID, endpoint ID, durasi, status, dan actor ID bila tersedia; tidak mencatat token/password/file bytes. OTel trace/metrics dapat diaktifkan dengan env. `/live` hanya membuktikan proses HTTP aktif; `/ready` memberi 503 bila PostgreSQL atau rate-store Redis wajib gagal. Semua HTTP/DB/Redis/S3 call memakai timeout dan `context.Context`.

## 5. Delivery, quality gate, dan penerimaan

### Urutan implementasi

1. **Repo/toolchain:** inisialisasi Go module, layout, konfigurasi env bertipe, Makefile/skrip Windows yang setara, lint, README, `.env.example`, dan perintah `initproject`. Commit `go.mod`/`go.sum` serta versi generator `sqlc`/Goose yang dikunci.
2. **Database:** buat migrasi SQL dan query sqlc, jalankan `sqlc generate`, seed role/permission dan bootstrap user opsional. Uji fresh DB dan upgrade DB fixture; satu job migrasi mengendalikan startup.
3. **HTTP/contract:** Chi+Huma, response/error envelope, registry, docs, health, CORS, request ID, auth, RBAC; lalu endpoint CRUD menurut matriks. Siapkan pembanding kontrak dengan Express.
4. **Operasional tambahan:** audit transaksi, rate limit memory/Redis, cache opsional, upload local/S3, cleanup orphan, UTC/time zone, logging dan OTel.
5. **Distribusi:** Dockerfile multi-stage non-root, Compose untuk app/PostgreSQL dan profile Redis/MinIO, service `migrate` one-shot, volume lokal, override port; manual run tetap tersedia. GitHub Actions menjalankan quality gate dan smoke test image.

### Test wajib

- `go test ./...`, `go test -race ./...`, `go vet ./...`, `gofmt` bersih, `sqlc generate` tanpa diff, `govulncheck ./...` tanpa finding reachable yang belum ditangani.
- HTTP contract test untuk seluruh 27 endpoint: method/path, DTO, status, envelope, auth, RBAC, pagination, audit/rate/cache policy, OpenAPI operation ID unik, dan coverage registry penuh.
- PostgreSQL integration test pada DB baru dan upgrade fixture: FK/unique/soft delete, query `sqlc`, rollback saat audit required gagal, snapshot redaksi, UTC round-trip.
- Redis test: disabled mode, cache miss/failure, invalidasi, fixed-window antar-replica, fail closed auth dan fail open public/internal.
- Storage test: local/S3 MinIO, file invalid/oversize, DB failure cleanup, orphan dry run/apply, traversal prevention.
- Compose dan image smoke test: migrasi harus selesai sebelum app start; `/live` dan `/ready` berbeda saat DB turun; port override dan profile opsional bekerja. Uji backup/restore DB staging sebelum menyebutnya siap production.

CI harus lulus pada Linux dengan Go versi terkunci, PostgreSQL 18, Redis dan MinIO ketika diperlukan; Windows lokal diuji untuk initproject, path lokal upload, dan build. Jalankan audit supply chain dan `govulncheck` pada tiap PR serta sebelum rilis. Tidak ada auto-deploy/push dari rencana ini.

### Kriteria selesai

Repo Go bisa di-clone dan dijalankan manual/Compose tanpa repo TypeScript; 27 operasi API beserta docs dan error envelope cocok dengan fixtures parity; setiap endpoint memiliki policy registry yang valid; audit wajib atomik; Redis dan S3 tetap opsional sesuai env; migrasi/seed terpisah dari replica HTTP; semua gate CI lulus; README menjelaskan setup, secret, migrasi, backup, dan batasan template. Setelah itu baru pertimbangkan tag rilis dan publikasi template Go.
