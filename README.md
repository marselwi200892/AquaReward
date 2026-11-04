# SehatAqua Stock Monitor

Memantau stok hadiah di `loyalty.sehataqua.co.id` dan mengirim notifikasi
Telegram saat stok tersedia. Berjalan sebagai cron GitHub Actions — tanpa
server, tanpa APK.

## Kenapa tanpa login?

Halaman `/rewards/<id>` mengambil data dari endpoint katalog TADA yang
**publik**:

```
GET /api/tada/v2/m-integration/items?programId=1000112251223&page=1&perPage=200&catalogType=rewardCatalog
```

Endpoint ini mengembalikan `hasStock` dan `stock` per varian tanpa cookie,
token, atau OTP WhatsApp. Login hanya diperlukan untuk menukar poin.

Kondisi "tersedia" = ada varian `statusVariant == "active"` dengan
`hasStock == true` atau `stock > 0`.

## Isi repo

| File | Fungsi |
| --- | --- |
| `main.go` | Poller + notifikasi Telegram |
| `.github/workflows/stock-check.yml` | Cron tiap 5 menit |
| `go.mod` | Modul Go |

## Setup (sekali saja)

### 1. Buat bot Telegram

1. Chat [@BotFather](https://t.me/BotFather) → `/newbot` → ikuti instruksi.
2. Simpan token, bentuknya `123456:ABC-DEF...`.
3. Kirim pesan apa saja ke bot yang baru dibuat (wajib, agar bot bisa
   mengirim ke kamu).

### 2. Dapatkan chat id

```bash
curl -s "https://api.telegram.org/bot<TOKEN>/getUpdates" | grep -o '"chat":{"id":[-0-9]*'
```

Angka `id` itulah `TELEGRAM_CHAT_ID`. Untuk grup, id biasanya negatif.

### 3. Push ke GitHub

```bash
cd /home/localadm/GolandProjects/SehatAqua
git init
git add .
git commit -m "Stock monitor SehatAqua"
git branch -M main
git remote add origin https://github.com/<USER>/<REPO>.git
git push -u origin main
```

### 4. Simpan secret

Repo → **Settings → Secrets and variables → Actions → New repository secret**:

- `TELEGRAM_BOT_TOKEN`
- `TELEGRAM_CHAT_ID`

### 5. Aktifkan Actions

Tab **Actions** → bila diminta, klik *I understand my workflows, go ahead and
enable them*. Scheduled workflow hanya berjalan dari branch default (`main`).

### 6. Uji manual

**Actions → Cek Stok SehatAqua → Run workflow**. Lihat log; bila item target
sedang ada stok, notifikasi Telegram langsung masuk.

## Menjalankan lokal (uji coba)

```bash
# tanpa kirim apa pun, hanya cetak isi notifikasi
DRY_RUN=true TARGET_ITEM_ID=38 go run .

# kirim sungguhan
TELEGRAM_BOT_TOKEN=123:abc TELEGRAM_CHAT_ID=123456789 TARGET_ITEM_ID=38 go run .
```

## Konfigurasi

Atur lewat environment variable (di workflow, bagian `env:`).

| Variabel | Default | Keterangan |
| --- | --- | --- |
| `TARGET_ITEM_ID` | `38` | Item yang dipantau (lihat id di URL `/rewards/38`) |
| `PROGRAM_ID` | `1000112251223` | Program TADA untuk SehatAqua |
| `BASE_URL` | `https://loyalty.sehataqua.co.id` | Basis situs |
| `REMIND_HOURS` | `6` | Kirim ulang pengingat tiap N jam selama masih ada stok |
| `STATE_FILE` | `state.json` | Lokasi file state |
| `DRY_RUN` | `false` | Cetak notifikasi tanpa mengirim |

Notifikasi dikirim saat:

- stok berubah dari habis → tersedia, atau
- masih tersedia dan sudah lewat `REMIND_HOURS` sejak notifikasi terakhir.

Saat stok kembali habis, **tidak** ada notifikasi (hanya dicatat di log).

## Catatan penting

- **Repo public disarankan.** Cron 5 menit ≈ 8.600 run/bulan. Repo public
  gratis tanpa batas menit; repo private punya kuota 2.000 menit/bulan yang
  bisa terlampaui. Kode ini tidak menyimpan rahasia apa pun — token hanya ada
  di Secrets.
- **Jadwal bisa terlambat.** GitHub tidak menjamin cron tepat waktu; saat
  ramai bisa mundur beberapa menit.
- **Workflow terjadwal dinonaktifkan setelah 60 hari tanpa aktivitas repo.**
  Bila itu terjadi, aktifkan lagi dari tab Actions (atau buat commit kecil
  setiap ~2 bulan).
- **Kemungkinan IP datacenter diblokir CloudFront.** Dari mesin lokal
  endpoint terbukti HTTP 200. Bila run GitHub Actions gagal dengan 403,
  jalankan binary yang sama di VPS/laptop ber-internet.
- State disimpan lewat `actions/cache`. Bila cache hilang, run berikutnya
  dianggap "pertama kali" dan akan mengirim notifikasi bila stok tersedia.
