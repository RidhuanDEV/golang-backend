# Laporan penyempurnaan arsitektur Go

Tanggal: 26 September 2026.

## Implementasi

- Use case auth, user, role, permission, upload dan audit dipisahkan dari transport; wiring eksplisit melalui `internal/app` dan `cmd/api`.
- Query CRUD, audit, cleanup dan seed memakai sqlc. UUID/null/time dipetakan eksplisit. Update memakai row lock dan snapshot transaksi; rollback memakai context cleanup terbatas yang tidak dibatalkan request.
- Huma terpasang pada Chi router request sebenarnya. Registrasi, validasi DTO, security, endpoint policy dan OpenAPI memakai satu jalur. Error bisnis bertipe dipetakan ke envelope HTTP; error validasi tidak menampilkan nilai password.
- Snapshot response Zod Express disimpan dalam `contracts/express-schemas.json`; registry fixture tetap mencakup 27 endpoint. Uji bentuk response tidak menyamakan UUID/token/timestamp acak dan tidak mengklaim seluruh regex Zod dijalankan di Go.
- Audit required atomik; optional/none diuji ketika writer gagal. Upload metadata failure memicu cleanup object dengan context detached; failed cleanup tercatat. Cache hit tetap memeriksa grant DB dan audit; cached payload yang tidak sesuai schema diabaikan.
- Initializer menyalin package baru dan dokumentasi operasi, mengganti module imports, serta mengecualikan Git, env sumber, data lokal dan prompt .NET. CI mencakup generated project, race tests, generation drift, vulnerability scan, Compose lokal/Redis/MinIO serta migrasi gagal menghalangi startup.
- Graceful shutdown menunggu request aktif sebelum dependency ditutup. Tidak ada perubahan schema atau migrasi baru; database tetap dimiliki Goose dan terpisah dari Express.

## Verifikasi

Gate lokal yang sudah lulus:

- `gofmt`, `go vet ./...`, `go test -race ./...` dengan PostgreSQL 18 dan Redis 8 nyata; tes tambahan upgrade terisolasi juga lulus dengan race detector.
- Regenerasi sqlc v1.31.1 menghasilkan hash yang sama; query dipisahkan per fitur. `git diff --check` dan actionlint v1.7.7 lulus.
- govulncheck v1.8.0: tidak ada vulnerability pada simbol yang terjangkau aplikasi atau package yang diimpor. Satu temuan pada module requirement tidak dipanggil aplikasi; ini bukan scan image atau jaminan seluruh dependency bebas temuan.
- Windows initializer menghasilkan module baru yang lulus tests, build, generation, migration dan startup manual (`/live`, `/ready`, OpenAPI).
- Compose image berhasil dibangun; database kosong bermigrasi, seed eksplisit dan readiness berhasil. Migrasi yang sengaja gagal mengembalikan exit 1 dan app tetap berstatus `created`.

Pengujian ulang Linux dan MinIO/S3 pada 26 September 2026 sudah lulus setelah engine kembali normal:

- Image MinIO server/mc dan backend terbaru berhasil dibangun. Compile dibatasi satu job, dua CPU Go dan soft memory limit 512 MiB; cache BuildKit menyimpan hasil compile. Batas ini berlaku pada build, bukan konfigurasi memori runtime aplikasi.
- Compose PostgreSQL, Redis dan MinIO berjalan; job migrasi dan seed eksplisit sukses. Readiness dan login berhasil dengan cache Redis serta rate store Redis aktif.
- Upload API mengembalikan 201 dan metadata GET sesuai. PostgreSQL menyimpan storage `s3` dan satu audit create. Objek nyata MinIO berukuran 68 byte dan SHA-256 sama dengan PNG yang dikirim.
- Audit wajib sengaja digagalkan melalui trigger PostgreSQL pada upload S3 nyata: respons 500, transaksi metadata batal, object baru terhapus. Bucket dan metadata tetap hanya memuat upload sukses sebelumnya. Trigger/function fault injection sudah dihapus.
- `scripts/verify-linux-initializer.sh` dijalankan dalam runner Go 1.27.1 Linux dengan batas container 1 GiB, dua CPU dan satu compile job. Proyek yang dihasilkan memakai module baru `example.com/smoke-api`; `go test ./...` lulus dengan PostgreSQL/Redis nyata, termasuk upgrade terisolasi, CRUD, audit, cache, rate limit, local upload/cleanup, symlink dan zona waktu. Build, migrasi serta probe `/live`, `/ready`, `/docs/openapi.json` pada binary hasil initializer lulus. Ini tes Linux biasa; bukti race detector berasal dari gate sebelumnya, bukan runner ini.
- Workflow CI memakai script initializer tersebut agar startup HTTP ikut menjadi gate. Actionlint dan `git diff --check` lulus.

Percobaan sebelumnya kehabisan memori dan menghasilkan engine HTTP 500. Pengujian ulang dengan compile terbatas berhasil; agen tidak menjalankan restart Docker Desktop. Hasil CI GitHub untuk perubahan ini harus diperiksa pada run setelah push. Tes upgrade membuat database baru versi 1, memasukkan role, menjalankan versi 2 dan memastikan ID/nama/timestamp fixture tetap sama; tidak bergantung pada urutan test CRUD.

MinIO registry lama gagal ditarik (401/manifest unavailable). Fixture lokal sekarang dibangun dari commit resmi MinIO `9e49d5e7a648f00e26f2246f4dc28e6b07f8c84a` dan mc `7394ce0dd2a80935aded936b09fa12cbb3cb8096`. Build pertama memerlukan unduhan source/dependency. Ini fixture integrasi lokal, bukan pilihan deployment object storage production.

Folder Windows `../modular-go-refine-smoke` dan `../modular-go-refine-smoke-final` dibuat untuk pengujian dan tidak menjadi bagian repo. Pembersihan rekursif folder smoke ditolak oleh pemeriksaan persetujuan otomatis dengan alasan blocked by policy; folder dibiarkan agar tidak melakukan tindakan alternatif yang mengabaikan penolakan tersebut. Proses API smoke telah dihentikan. Project Compose `modular-go-refine-gate` dan `modular-go-refine-compose` beserta volume pengujiannya sudah dibersihkan. Container `modular-go-refine-postgres`, `modular-go-refine-redis` dan runner `modular-go-refine-linux-smoke` sudah dihapus. PNG sementara juga sudah dihapus melalui path file eksplisit. Container proyek lain tetap berjalan; cache build dan image pengujian dipertahankan.

## Batas dan keputusan

Projection role create/update dan audit perubahan nama memakai atribut dasar role; grant permission dicatat terpisah agar tidak memberi snapshot permission kosong yang menyesatkan. PostgreSQL 18 dapat menghasilkan SQLSTATE `23001` untuk RESTRICT selain `23503`; keduanya dipetakan melalui error terstruktur.

Optional issuer/audience mempertahankan konfigurasi lama ketika kosong. Mengaktifkannya mengharuskan token baru. Input unknown ditolak dan bcrypt dibatasi 72 byte; ini keputusan keamanan yang didokumentasikan, bukan klaim kesamaan semua perilaku Express.

Pengujian lokal tidak membuktikan deployment production, SLA, load target, backup restore, alerting atau secret rotation. Runbook ada di `docs/OPERATIONS.md`. Repo Express tidak diubah dalam implementasi ini.
