![Guangyue Panel: paketler, düğümler ve siteler tek panelde](docs/assets/banner.svg)

# Guangyue Panel · 广月面板

**Tek alan adı. Paketler, düğümler ve siteler tek panelde.**

Proxy hizmetlerini ve ağ kaynaklarını kendi sunucunuzda yönetebileceğiniz bir panel. VLESS Reality/Vision ve Hysteria 2 kurun, çıkış IP’lerini ve bağımsız siteleri yönetin, kullanıcıların paketlerine göre abonelik sunun. Go ve Vue 3 ile geliştirilmiştir: Lite bağımsız bir site, Pro ise birden fazla sitenin merkezi yönetimi içindir.

[![Son sürüm](https://img.shields.io/github/v/release/wudi000888-svg/guangyue-panel)](https://github.com/wudi000888-svg/guangyue-panel/releases/latest)
[![CI](https://github.com/wudi000888-svg/guangyue-panel/actions/workflows/ci.yml/badge.svg)](https://github.com/wudi000888-svg/guangyue-panel/actions/workflows/ci.yml)
[![Lisans: LGPL-3.0](https://img.shields.io/badge/License-LGPL--3.0-blue.svg)](LICENSE)

<!-- language-navigation -->
[简体中文](README.md) · [English](README_EN.md) · [فارسی](README.fa_IR.md) · [العربية](README.ar_EG.md) · [Español](README.es_ES.md) · [Русский](README.ru_RU.md) · [Türkçe](README.tr_TR.md)
<!-- /language-navigation -->

[Kurulum](docs/INSTALL.md) · [Belgeler](docs/README.md) · [Sürümler](https://github.com/wudi000888-svg/guangyue-panel/releases) · [Kullanım kılavuzu](docs/USER_GUIDE.md) · [Sorun bildirin](https://github.com/wudi000888-svg/guangyue-panel/issues/new/choose)

## Başlangıç

Bir sunucu ve alan adı hazırlayın. Kurulum sihirbazı ortamı denetler; uygulamayı, proxy çekirdeklerini, sertifikaları ve gerekli veritabanını hazırlar. **Varsayılan yöntem Docker’dır; doğrudan systemd ile kurulum da seçilebilir.** Panel ve varsayılan düğümler aynı alan adını kullanabilir.

- Alan adını doğrudan sunucuya yönlendirin ve **CDN proxy’sini kapatın**. Cloudflare’da **DNS only** seçeneğini kullanın; turuncu bulut kapalı olmalıdır.
- Genel erişime açık **TCP 443 ve UDP 443** portları boş olmalı, güvenlik duvarında ve bulut güvenlik grubunda izin verilmelidir. Kurulum başka bir genel port seçmez veya mevcut HTTPS hizmetini devralmaz.
- root veya sudo erişimi olan tam bir Linux amd64/x86_64 sunucu kullanın. Ön denetim Debian 12/13 ve Ubuntu 22.04/24.04’ü kabul eder; systemd kurulumunun sanal makine testleri Ubuntu 22.04/24.04’ü kapsar. Bir sistemin kabul edilmesi tüm yapılandırmalarının test edildiği anlamına gelmez.
- Docker şu anda yalnızca IPv4 üzerinden yayın yapar: AAAA olmadan A kayıtları kullanın. IPv6 için systemd kurulumunu seçin. Başlangıç için Lite’a 1 CPU / 1 GiB, Pro’ya 2 CPU / 2 GiB ayırın; veri ve yedekler için ek alan bırakın.

Aşağıdaki bloğun tamamını sunucunun terminaline yapıştırın:

```bash
gy_install_file=$(mktemp)
curl --proto '=https' --proto-redir '=https' -fsSL \
  https://github.com/wudi000888-svg/guangyue-panel/releases/latest/download/install.sh \
  -o "$gy_install_file" && sudo bash "$gy_install_file"
rm -f "$gy_install_file"
```

Sihirbaz, planı onayınıza sunmadan önce DNS’i, portları, kaynakları ve mevcut Nginx/Docker yapılandırmasını denetler. Sertifikalar TCP 443 üzerinden TLS-ALPN-01 ile alınır ve yenilenir; Nginx’in mevcut HTTP 80 hizmeti korunur. Bileşenler ve imajlar seçilen sürümden alınır ve sağlama toplamlarıyla doğrulanır.

Kurulumdan sonra belirtilen özel ilk hesap dosyasını okuyun, yönetici parolasını değiştirin ve gerçek bir istemciyle VLESS ile HY2’yi test edin. Kayıt açılışını, paketlerin satışa sunulmasını ve e-postayı yapılandırın. Bağımsız sitelerde ödeme modülü varsayılan olarak açıktır; harici kanallar yine de geçerli kimlik bilgileri ve kurulum gerektirir. Bir alt site ilk kez ana sitenin yönetimine alındığında ödemeleri varsayılan olarak kapanır. Paket ancak satışa sunulan teklifi etkinse satın alınabilir.

Mevcut sitelerde verilerin üzerine yeniden kurulum yapmak yerine [güncelleme ve geri alma](docs/UPDATES.md) kullanın. [Kurulum kılavuzu](docs/INSTALL.md), salt okunur `--check`, kesintiden sonra `--resume` ve dağıtım sınırlarını açıklar.

## Lite mı Pro mu?

| | Lite | Pro |
| --- | --- | --- |
| Temel kullanım | Bağımsız bir site | Diğer siteleri yöneten ana site |
| Kalıcı veri | SQLite | PostgreSQL |
| Önbellek ve işler | Sınırlı yerel önbellek ve kalıcı iş kuyruğu | Redis ve kalıcı iş kuyruğu |
| Ana siteye bağlanma | Alt site bağlantı anahtarı oluşturma | Alt site olarak çalışırken anahtar oluşturma |
| Alt siteleri yönetme | Yok | Alt sitenin bağlantı anahtarını içe aktarma |
| Kurulum | Docker veya systemd | Docker veya systemd |

**Bağlantı anahtarını alt site oluşturur, Pro içe aktarır.** Kayıt dosyası gerekmez. Anahtarlar süreli olabilir veya iptal edilene kadar geçerli kalabilir. Her site yöneticilerini, hesaplarını, verilerini ve yerel hizmetlerini korur; yerel yönetici erişimi iptal edebilir. Ayrıntılar: [sürümler](docs/EDITIONS.md) ve [site yönetimi](docs/BUSINESS-SITES.md).

## Özellikler

- **Düğümler ve çıkış IP’leri:** yerel VLESS Reality/Vision ve HY2, HTTP CONNECT/SOCKS5 çıkışları ve kullanım izniniz olan sağlayıcı abonelikleri. Genel kaynaklar ve toplama araçları isteğe bağlıdır.
- **Alt siteler:** yönetim izinleri, düğüm bağlama, ortak kullanım bütçeleri, yeniden etkinleşme süreleri, örneklenen trafik/bağlantı verileri ve Pro’dan desteklenen sürüm güncellemeleri.
- **Paketler ve abonelikler:** kaynak grubunun tamamı veya grup içindeki belirli düğümler, kota, süre ve pakete özel düğüm çarpanları. Yeni paket, öncekinin kalan süresini duraklatır; önceki haklar kuyruk sırasına göre devam eder.
- **Satış ve destek:** siparişler, bakiye, kullanım kodları, yönetici onaylı iadeler, destek talepleri, SMTP, EPay V1 ve Stripe Checkout. Harici hizmetler için ayrıca yapılandırma ve kabul testi gerekir.
- **Kripto ödemeleri:** bu ödeme yöntemi seçildiğinde siparişe özel kalıcı adres, bakiyeler, fon bulunan adresler, Gas tahmini ve onaylı fon toplama. Ethereum, BNB Smart Chain, Plasma ve Solana’daki desteklenen USDT/USDC varlıklarında otomatik ödeme doğrulama ve fon toplama bulunur. L2 yapılandırması bu otomatik işlemlerin desteklendiği anlamına gelmez.
- **Günlük kullanım:** Çince ve İngilizce arayüz, açık/koyu tema, mobil düzen, özelleştirilebilir menü, bildirimler, düğüm denetimleri, ülkeye göre erişim, yedekleme ve sürüm yönetimi.

İşlem ücretleri yalnızca yönetici fon toplamayı onayladıktan sonra ödenir. Otomatik ücret desteği, gerçek bir sipariş adresi ve izin listesindeki seçili stablecoin için **0,1’den kesin olarak büyük** bakiye gerektirir. EVM’de yalnızca eksik tutar tamamlanır; Solana sabit SOL ücret ödeyicisini kullanır. Onaylanan ücret üst sınırı aşılırsa işlem durur ve manuel inceleme bekler.

Alt site kaynakları iki farklı trafik yolu sunar:

| Kaynak | Trafik yolu |
| --- | --- |
| Bağlanan alt site düğümü | İstemci doğrudan alt düğüme bağlanır; trafik ana siteden geçmez. |
| Alt site çıkış IP’si | İstemci ana site düğümüne bağlanır; ana site trafiği alt sitenin IP çıkışından geçirir. |

## Kapsam ve sınırlar

Mevcut paketler systemd kullanan tam Linux amd64 sunucular içindir. SLA, siteler arasında otomatik yük devretme veya çoklu örnek yüksek erişilebilirliği taahhüt edilmez. Aktarım hızı proxy çekirdeğine ve ağ yoluna bağlıdır; Pro yavaş bir hattı hızlandırmaz. İstatistikler belirli aralıklarla örneklenir. Etkin paketin düğüm, çarpan ve protokol değişiklikleri sunucuda eşitlenir; kota, geçerlilik süresi ve sıradaki hakların anlık görüntüleri değişmez. Eski aboneliği önbellekte tutan istemci aboneliği yenilemelidir.

Trust Wallet Core kullanan dahili cüzdan, küçük işletim bakiyeleri için bir sıcak cüzdandır. Şifreleme, imzalama yetkisini ele geçirilmiş sunucudan ayırmaz; fonları düzenli olarak aktarın. Harici xpub içe aktarımı sunucuda imza atmadan izleme sağlar. Bağımsız güvenlik denetimi yapıldığı iddia edilmez. Genel RPC ve fiyat kaynakları kesilebilir; ücret tahmini son maliyeti garanti etmez veya gerçek ağ testinin yerini almaz.

## Belgeler ve katkı

| Amaç | Kılavuz |
| --- | --- |
| Kurulum ve Docker bakımı | [Kurulum](docs/INSTALL.md) · [Docker](docs/DOCKER.md) |
| Site ve düğüm bağlama | [Siteler](docs/BUSINESS-SITES.md) · [Düğüm bağlama](docs/NODE_MOUNTS.md) |
| Paket ve ödeme ayarları | [Paketler](docs/PLANS.md) · [İngilizce ticaret kılavuzu](docs/COMMERCE_EN.md) · [Kripto fon yönetimi](docs/CRYPTO_TREASURY.md) |
| Sorun giderme, yedekleme ve güncelleme | [İşletim](docs/OPERATIONS.md) · [Güncellemeler](docs/UPDATES.md) · [Doğrulama](docs/VALIDATION.md) |
| Geliştirme veya güvenlik bildirimi | [Katkı](CONTRIBUTING.md) · [Güvenlik](SECURITY.md) |

Bu sayfa projenin çevrilmiş tanıtımıdır; **arayüz yalnızca Çince ve İngilizce sunulmaktadır**. Ayrıntılı teknik belgelerin çoğu Çince, ticaret kılavuzunun ayrıca İngilizce sürümü vardır. Sorun bildirirken sürümü, Lite/Pro seçimini, kurulum yöntemini ve örnek verilerle yeniden üretme adımlarını ekleyin. Anahtarları, üretim yapılandırmalarını veya abonelik kimlik bilgilerini paylaşmayın.

## Lisanslar ve dağıtım

Guangyue Panel, 0.15.0’dan itibaren **LGPL-3.0-only** kullanır; [ilk MIT bildirimi](licenses/guangyue-legacy-MIT.txt) korunur. Hysteria ve yaması MIT, Xray MPL-2.0, Mihomo GPL-3.0 lisanslıdır. Atıflar ve ilgili kaynak kodu için [üçüncü taraf bildirimlerine](THIRD_PARTY_NOTICES.md) bakın. Çok dilli tanıtım yapısı [3x-ui](https://github.com/MHSanaei/3x-ui) projesinden esinlenmiştir.

Sürümler çalışma paketlerini, kurulum aracını, Docker imaj arşivlerini, sağlama toplamlarını ve bağımlılık bilgilerini içerir; bkz. [sürüm hazırlama](docs/RELEASE.md). Sağlama toplamları değişiklik ve bozulmayı saptar, yayımlayana duyulan güvenin yerini almaz.
