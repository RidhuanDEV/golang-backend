# Rencana penyempurnaan arsitektur template Go

Tanggal: 26 September 2026. Status: perubahan kode diterapkan dan gate lokal Linux/S3 lulus setelah pengujian ulang. Verifikasi lokal dan batas bukti dicatat dalam [laporan implementasi](GO-ARCHITECTURE-IMPLEMENTATION-REPORT.md). Workflow CI diperbarui; eksekusi GitHub menunggu commit/push pengguna.

## 1. Tujuan

Rapikan template Go menjadi backend modular yang mudah diperluas tanpa mengubah kontrak API yang sudah disepakati. Repo kerja adalah `template-JS/modular-golang`, remote `https://github.com/RidhuanDEV/golang-backend`. Repo Express tetap menjadi referensi kontrak; database Go tetap terpisah dan hanya dikelola migrasi Goose.

Dynamic berarti mengubah konfigurasi environment dan melakukan redeploy. Tidak membuat UI admin atau reload policy saat runtime. Perubahan tidak otomatis di-commit, di-push, atau di-deploy; ikuti instruksi pengguna pada sesi implementasi.

## 2. Temuan source yang menjadi dasar

- `cmd/` dan `internal/` sudah memisahkan executable dari package aplikasi.
- `internal/httpapi/service.go` dan `entities.go` masih menyatukan auth, RBAC, audit, CRUD, dan query SQL dalam satu service.
- `internal/db/queries/auth.sql` baru berisi tiga query sqlc. Banyak query lain masih inline dengan scan manual.
- `register` di `internal/httpapi/server.go` mendaftarkan Huma pada router docs, tetapi request aktual diproses handler Chi terpisah dengan validasi manual.
- `contracts/express-endpoints.json` menjaga parity method/path/status/access/policy, belum merupakan bukti parity seluruh response atau seluruh alur CRUD.
- Test PostgreSQL sudah mencakup beberapa transaksi, audit rollback, dan upload. Test belum mencakup seluruh skenario bisnis dan kegagalan infrastruktur.

CI sebelumnya lulus, tetapi kelulusan itu terbatas pada gate yang memang tersedia. Verifikasi keadaan repo dan baseline test kembali sebelum mengedit.

## 3. Arsitektur yang dituju

Gunakan package berdasarkan fitur dan dependency eksplisit. Hindari interface untuk setiap struct, generic repository, service locator, dan package `utils` yang menjadi tempat semua fungsi.

```text
cmd/
  api/                     # composition root, lifecycle HTTP
  migrate/                 # release job Goose
  seed/                    # seed eksplisit
  cleanup-uploads/
  initproject/
internal/
  auth/                    # credentials, JWT, authentication, permission check
  user/                    # aturan bisnis dan projection user
  role/                    # role dan assignment permissions
  permission/
  upload/                  # orkestrasi object + metadata + kompensasi
  audit/                   # typed event, redaction, transactional writer
  httpapi/
    registry.go            # ID/policy/metadata endpoint
    server.go              # router dan middleware HTTP
    response.go            # envelope dan mapping error
    auth.go                # DTO dan handler transport per fitur
    users.go
    roles.go
    permissions.go
    uploads.go
  db/
    db.go                  # connection dan transaction helper
    migrations/
    queries/               # SQL per fitur
    sqlc/                  # generated, jangan diedit manual
  config/
  cache/
  ratelimit/
  storage/
  telemetry/
  timeutil/
contracts/
  express-endpoints.json
  fixtures/                # request, response, status dan normalisasi eksplisit
docs/
scripts/
```

Aturan dependency:

1. `cmd/api` merangkai database, layanan, storage, cache, limiter, dan HTTP. Constructor memvalidasi dependency wajib, mengembalikan error saat konfigurasi tidak sah.
2. `httpapi` mengubah DTO HTTP menjadi input use case dan error use case menjadi HTTP. Package fitur tidak mengimpor `httpapi`, Chi, atau Huma.
3. Package fitur memakai query sqlc dan dependency kecil yang dibutuhkan. Jangan membuat dependency melingkar antarfitur; pindahkan kontrak kecil ke package pemiliknya bila memang digunakan lintas fitur.
4. `audit` menerima actor/request metadata bertipe sebagai parameter, bukan mengambil request HTTP atau context key milik transport.
5. Domain/application error tidak membawa HTTP status. Mapping 400/401/403/404/409/413/429/500/503 berada di transport.
6. Gunakan tipe konkret untuk input/output/event. Generic constraint `any` dan parameter yang diwajibkan API library diperbolehkan; jangan gunakan `map[string]any` sebagai kontrak bisnis atau melempar payload antar-layer tanpa tipe.

## 4. Tahapan implementasi

### Tahap A — Bekukan kontrak dan baseline

- Baca AGENTS, README, schema, registry, DTO, handlers, services, dan tests kedua template.
- Simpan golden fixture dari kontrak Express yang benar-benar dibaca. Tandai asal dan tanggal snapshot; jangan mengambil Go sebagai sumber fixture parity Go sendiri.
- Catat 27 endpoint, status sukses/gagal, pagination, fields projection, role/permission nesting, format waktu, dan aturan akses.
- Nyatakan normalisasi ID, token, timestamp, serta urutan array yang tidak dijamin. Normalisasi tidak boleh menyembunyikan field hilang, nullability berbeda, atau error bisnis berbeda.
- Jalankan baseline test/vet/generation/Compose dan simpan batas bukti dalam laporan.

### Tahap B — Query bertipe secara menyeluruh

- Pindahkan query user, role, permission, audit, upload, dan cleanup ke file SQL sqlc.
- Gunakan `Queries.WithTx(tx)` untuk query dalam transaksi; helper transaksi menangani rollback pada error/panic dan mengembalikan kegagalan commit.
- Query filter/sort memakai allowlist, parameter SQL, dan urutan pagination deterministik. Untuk sort dinamis gunakan query eksplisit atau varian yang terbatas; jangan menyambung identifier dari input pengguna.
- Gunakan tipe UUID/null/time dari generated code secara sadar dan conversion eksplisit ke domain/DTO. Cek `rows.Err`, error scan, constraint violation, serta zero rows.
- Jangan mengubah migrasi lama yang pernah diterapkan. Bila schema perlu berubah, tambahkan migrasi versioned dan uji upgrade dengan data lama.
- CI menjalankan generator versi terkunci dan gagal bila hasil generation berbeda dari file committed.

### Tahap C — Pecah service menurut fitur

- Extract auth/RBAC, user, role, permission, upload, dan audit secara bertahap sambil mempertahankan test dan kontrak.
- Auth membaca user aktif dan grant terkini dari DB; jangan cache keputusan izin.
- Update/delete mengambil snapshot sebelum/sesudah di transaksi yang sama; gunakan locking atau strategi concurrency eksplisit untuk perubahan yang saling bersaing.
- Audit required wajib atomik dengan mutasi; optional tidak menggagalkan mutasi yang sudah commit, tetapi kegagalan tercatat; none tidak menulis audit.
- Typed projection audit tidak memuat password/hash/token/credential; redaction rekursif menjadi pertahanan tambahan.
- GET required hanya boleh jika producer audit dapat dijamin sebelum respons/cache hit. Tolak konfigurasi yang tidak dapat dipenuhi.
- Upload menulis object, kemudian metadata dan audit; bila DB gagal, lakukan kompensasi dengan context cleanup yang terbatas dan tidak sudah dibatalkan request. Kegagalan kompensasi terlihat di log dan bisa dipulihkan cleanup orphan.

### Tahap D — Satu jalur Huma untuk docs dan request

- Mount Huma pada router request sebenarnya. Wrapper registry mendaftarkan handler bertipe, bukan dummy handler untuk docs.
- Input DTO Go yang sama menghasilkan schema docs dan validasi runtime body/path/query/multipart.
- Pertahankan error envelope template melalui error adapter Huma. Verifikasi seluruh jalur framework: binding, validation, auth, panic, body limit, dan rate limit.
- Validasi lintas field/DB tetap berada pada use case; validasi format/ukuran/default pada DTO. Hindari validasi ganda yang berbeda aturan.
- Metadata security, operation ID, successful response, error response, multipart, audit/rate/cache dihasilkan dari registrasi yang sama.
- Audit dan rate enforcement harus tetap berjalan pada cache hit. Middleware dan wrapper mendukung cancellation dan observability.
- Coverage startup memastikan setiap ID hanya terpasang sekali; test memeriksa duplicate route, missing ID, unknown override, dan OpenAPI runtime parity.

### Tahap E — Lengkapi test yang membuktikan perilaku

- CRUD sukses dan gagal setiap fitur: binding, UUID/email/password, pagination/projection, not found, unique conflict, FK conflict, permission assignment, soft delete, dan user non-admin.
- Auth: JWT salah algoritma/signature/issuer/audience/expiry sesuai konfigurasi, user terhapus, grant dicabut, role berubah, hash tidak keluar di API/audit/log.
- Audit: rollback required, optional failure, none, before/after akurat, concurrency, cache hit, dan konfigurasi GET yang ditolak.
- Redis: disabled tanpa koneksi, cache corrupt/failure, invalidasi setelah commit, limiter atomic dua instance, expiry, fail closed auth, fail open public/internal yang terdokumentasi.
- Upload: lokal dan S3 nyata dalam CI, traversal/symlink yang relevan, MIME signature, oversize, request canceled, DB failure cleanup, failed cleanup, orphan dry run/apply.
- Waktu: UTC DB round-trip, input wajib offset, WIB/WITA/WIT dan zona dengan daylight saving; tanggal bisnis tanpa waktu diuji terpisah.
- Deployment: migrasi gagal menghalangi startup Compose, fresh DB/upgrade, `/live` vs `/ready` ketika dependency gagal, graceful shutdown dengan request aktif.
- Tests use case memakai fake hanya pada boundary yang perlu; integrasi DB memakai PostgreSQL nyata. Jangan mock EF/SQL semantics atau menganggap build sebagai E2E.

### Tahap F — Template dan operasi

- Update initializer allowlist, module import rewriting, contoh endpoint, README, generator sqlc, Compose, dan scripts Linux/Windows sesuai package baru.
- Smoke test proyek hasil initializer: restore dependencies, generation, build, tests, migration dan startup; hindari referensi balik ke checkout template.
- Tambahkan runbook backup/restore PostgreSQL dan objects, deployment release job, secrets, proxy trust, dependency outage, serta rollback aplikasi yang kompatibel schema.
- OTel tidak mengirim data sensitif; request route label rendah cardinality. Bedakan observability yang benar-benar terinstrumentasi dari sekadar export provider.
- Dokumentasikan batas: template tidak membuktikan SLA, kapasitas, compliance, atau production deployment.

## 5. Gate penerimaan

- Method/path, sukses/error envelope, status, pagination, permission nesting, waktu, dan seluruh policy tetap sesuai fixture yang disetujui.
- Package fitur tidak bergantung pada transport; `httpapi` tidak berisi SQL atau aturan bisnis lintas modul.
- Semua query aplikasi statis melalui sqlc; tidak ada scan manual berulang sebagai kontrak utama.
- Request Huma aktual dan OpenAPI berasal dari DTO/registrasi yang sama; test invalid input memverifikasi docs benar-benar berlaku.
- Audit required atomik dan upload compensation dapat dibuktikan pada kegagalan nyata.
- `gofmt`, `go vet`, `go test -race ./...`, sqlc no diff, govulncheck, Linux/Windows initializer smoke, dan Compose/PostgreSQL/Redis/S3 CI lulus.
- Laporan akhir menyebut file berubah, keputusan arsitektur, test, batas test, migrasi baru, dan pekerjaan operasional yang membutuhkan staging.
- Sebutan siap production untuk deployment tertentu baru diberikan setelah konfigurasi secrets/TLS/proxy, backup restore, load target, alerting dan recovery diuji pada infrastrukturnya.

## 6. Referensi

- [Go module layout](https://go.dev/doc/modules/layout)
- [Huma request validation](https://huma.rocks/features/request-validation/)
- [sqlc dengan pgx](https://docs.sqlc.dev/en/latest/guides/using-go-and-pgx.html)
- [Go database transactions](https://go.dev/doc/database/execute-transactions)

## 7. Prompt singkat untuk implementasi Go

Implementasikan `docs/GO-ARCHITECTURE-REFINEMENT-PLAN.md` pada repo Go ini secara bertahap sampai gate penerimaan terpenuhi. Baca source dan instruksi repo lebih dahulu; pertahankan dirty worktree, kontrak Express, database Go terpisah, serta dynamic melalui env dan redeploy. Mulai dari fixture kontrak, lanjut sqlc menyeluruh, pemisahan package fitur, lalu Huma runtime yang sama dengan docs dan test kegagalan. Jangan menyebut seluruh parity atau production readiness terbukti hanya karena build/CI lulus. Jangan commit, push, publish atau deploy tanpa instruksi eksplisit pada sesi ini.
