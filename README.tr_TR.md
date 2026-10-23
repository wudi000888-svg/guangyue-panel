<!-- language-navigation -->
[简体中文](README.md) · [English](README_EN.md) · [فارسی](README.fa_IR.md) · [العربية](README.ar_EG.md) · [Español](README.es_ES.md) · [Русский](README.ru_RU.md) · [Türkçe](README.tr_TR.md)
<!-- /language-navigation -->

<div align="center">

<img src="docs/assets/banner.svg" alt="Guangyue Panel: tek adımda kurulum, paket yönetimi, IP ve alt site yönetimi" width="960" />

# Guangyue Panel · 广月面板

**Tek bir alan adıyla başlayın; düğümleri, paketleri ve alt siteleri aynı çalışma alanında toplayın.**

[![Son sürüm](https://img.shields.io/github/v/release/wudi000888-svg/guangyue-panel?color=2563eb)](https://github.com/wudi000888-svg/guangyue-panel/releases/latest)
[![CI](https://github.com/wudi000888-svg/guangyue-panel/actions/workflows/ci.yml/badge.svg)](https://github.com/wudi000888-svg/guangyue-panel/actions/workflows/ci.yml)
[![LGPL-3.0](https://img.shields.io/badge/License-LGPL--3.0-blue.svg)](LICENSE)

[Kurulum](docs/INSTALL.md) · [Son sürümü indir](https://github.com/wudi000888-svg/guangyue-panel/releases/latest) · [Kullanım kılavuzu](docs/USER_GUIDE.md) · [Belgeler](docs/README.md)

</div>

Guangyue, Go ve Vue 3 ile geliştirilmiş, kendi sunucunuzda barındırdığınız bir proxy hizmeti ve ağ kaynağı yönetim panelidir. Uygun bir sunucu ve ona yönlendirilmiş bir alan adı hazırladığınızda VLESS Reality/Vision ile Hysteria 2'yi kurabilir, ardından paket abonelikleri, çıkış IP'leri, bağımsız alt siteler, siparişler, destek talepleri ve tahsilatı panelden yönetebilirsiniz.

Tek bir siteyle başlayan proxy hizmeti işletmecileri, birden fazla bağımsız siteyi merkezden yönetmesi gereken yöneticiler ve üyelerine çıkış IP'si atayan ekipler için uygundur. Lite bağımsız işletim içindir; Pro bunu ana site konsoluna genişletir. İki sürüm de aynı etkileşimli betikle kurulur.

## Başlangıç

### Kurulumdan önce

- **Alan adı bu sunucuya zaten yönlendirilmiş olmalı ve CDN proxy'si kapalı olmalıdır.** Cloudflare'de "DNS only" seçeneğini seçin ve turuncu bulutu kapatın.
- root / sudo yetkisine sahip, **systemd kullanan tam bir Linux amd64 (x86_64) sunucu** kullanın. Ön kontrol Debian 12/13 ve Ubuntu 22.04/24.04'ü kabul eder; sanal makinelerdeki yerel kurulum testleri şu anda Ubuntu 22.04/24.04'ü kapsar. Bir işletim sisteminin kabul edilmesi, tüm sunucu yapılandırmalarının test edildiği anlamına gelmez.
- **Herkese açık TCP 443 ve UDP 443 boş olmalı ve izinli olmalıdır**; buna bulut güvenlik grubu ve sunucu güvenlik duvarı dahildir. Betik her zaman 443'ü kullanır ve mevcut bir HTTPS hizmetini taşımaz; HTTP 80 üzerindeki mevcut Nginx hizmeti kalabilir.
- **Varsayılan Docker'dır; yerel kurulum isteğe bağlıdır.** Docker şu anda yalnızca IPv4 üzerinden yayın yapar: yalnızca A kaydı yapılandırın, AAAA eklemeyin; IPv6 gerekiyorsa çift yığınlı yerel kurulumu seçin. Docker, Linux 5.4 veya daha yenisini gerektirir.
- Başlangıç için Lite'ta 1 CPU / 1 GiB, Pro'da 2 CPU / 2 GiB önerilir; imajlar, veriler ve yedekler için ek alan bırakın. Bu bir kurulum önerisidir; eşzamanlı kullanıcı sayısı veya bant genişliği taahhüdü değildir.

### Kurulum betiğini çalıştırın

Aşağıdaki bloğun tamamını sunucu terminaline kopyalayın:

```bash
gy_install_file=$(mktemp)
curl --proto '=https' --proto-redir '=https' -fsSL \
  https://github.com/wudi000888-svg/guangyue-panel/releases/latest/download/install.sh \
  -o "$gy_install_file" && sudo bash "$gy_install_file"
rm -f "$gy_install_file"
```

Sihirbaz Lite / Pro, alan adı ve kurulum yöntemini seçmenizi ister; sistemi, kaynakları, DNS'i, portları ve mevcut Nginx / Docker yapılandırmasını denetler, ardından onayınız için bir kurulum planı gösterir. Çalışma bileşenleri ve Docker imajları seçilen sürümden alınır ve sağlama toplamlarıyla doğrulanır; imaj deposu hesabı gerekmez. Sertifikalar TCP 443 üzerinde TLS-ALPN-01 ile otomatik olarak alınır ve yenilenir.

Kurulumdan sonra betiğin gösterdiği şekilde özel başlangıç hesabı dosyası `/var/lib/guangyue/initial-owner.json` dosyasını okuyun, oturum açıp yönetici parolasını değiştirin ve VLESS ile HY2'yi gerçek bir istemciyle test edin. Kaydı, paket yayınını ve ödeme kanallarını gerektiği gibi yapılandırın; harici ödeme ve e-posta hizmetleri için kendi geçerli yapılandırmanız gerekir.

`--check` salt okunur bir ön kontrol yapar, `--resume` yarıda kalan kurulumu sürdürür. Mevcut siteler, verilerin üzerine yeni kurulum yapmak yerine [çevrim içi güncelleme ve geri alma](docs/UPDATES.md) yolunu kullanmalıdır. Ayrıntılı gereksinimler, hata kurtarma ve kurulum sınırları için [kurulum kılavuzuna](docs/INSTALL.md) bakın.

## Lite mı Pro mu?

| | Lite | Pro |
| --- | --- | --- |
| Varsayılan kullanım | Bağımsız site; alt site olarak da çalışabilir | Alt siteleri merkezden yöneten ana site |
| Veritabanı | SQLite | PostgreSQL |
| Önbellek ve görevler | Sınırlı süreç içi önbellek, kalıcı görev kuyruğu | Redis önbelleği, kalıcı görev kuyruğu |
| Site özellikleri | Düğümler, IP havuzu, paketler, kullanıcılar, siparişler ve destek talepleri | Lite ile aynı, ayrıca merkezi yönetim |
| Bağlantı | Ana sitenin içe aktarması için alt site belirteci üretir | Bağlanmak için alt site belirtecini yapıştırın |
| Kurulum | Docker / yerel systemd | Docker / yerel systemd |

Kurulum için kayıt dosyası veya önceden oluşturulmuş site grupları gerekmez. **Alt site belirteci üretir, Pro ana site onu yapıştırır.** Belirtecin bir son kullanma tarihi olabilir ya da iptal edilene kadar geçerli kalabilir. Bağlanan alt site yerel yöneticilerini, hesaplarını, veritabanını ve bağımsız hizmetlerini korur; erişim iptal edilebilir. İlk kez yönetime alınan bir alt sitede ödeme modülü varsayılan olarak kapalıdır; yerel yönetici gerekirse etkinleştirebilir. Ayrıntılar için [sürüm farkları](docs/EDITIONS.md) ve [alt site yönetimi](docs/BUSINESS-SITES.md) belgelerine bakın.

## Neler yapabilirsiniz

| Alan | Yetenekler |
| --- | --- |
| Düğümler ve abonelikler | VLESS Reality/Vision, HY2; Raw / Base64 / Mihomo abonelikleri, kopyalama ve QR kodları; platforma göre resmî istemci bağlantıları ve isteğe bağlı olarak siteniz üzerinden indirme aktarımı |
| Paket işletimi | Kaynak düğüm grubuna veya grup içindeki seçili düğümlere göre yetki; kota ve geçerlilik süresi, ücretsiz paketler, satın alma sınırı, paket kuyruğu; paket içi düğüm çarpanları 0,5 adımlarla, en fazla 10× |
| Siparişler ve destek | Site içi bakiye, kullanım kodları, siparişler, iade talebi / iptali / onayı / reddi, görselli destek talepleri, yöneticiye özel iç notlar ve site içi bildirimler |
| IP kaynakları | HTTP CONNECT / SOCKS5 çıkışları, yetkili sağlayıcı abonelikleri, kaynakların otomatik güncellenmesi, kalite kontrolleri, DNS ve IPv6 politikaları; herkese açık kaynak girişi varsayılan olarak gizlidir ve geçerli tarayıcı tercihiyle gösterilebilir |
| Alt site yönetimi | Belirteçle bağlantı, yetkiler ve hizmet işlemleri, bekleme süresi sonrası yeniden açılan ortak bütçeler, düğüm bağlama, alt site IP çıkışları, desteklenen sürüm denetimi, güncelleme ve geri alma |
| İzleme | Kullanıcı trafik grafikleri, VLESS oturumu ve HY2 bağlantısı örneklemesi, hizmet durumu, görev ilerlemesi ve yeniden başlatma sonrası kurtarma |
| E-posta ve ödemeler | SMTP e-posta, EPay V1, Stripe Checkout, desteklenen zincirlerde izinli varlıklar için kripto tahsilat; harici kanallar yapılandırma ve kabul testi gerektirir |
| Cüzdan yönetimi | Yerleşik HD sıcak cüzdan / harici açık anahtar içe aktarma, kalıcı sipariş adresleri, bakiyeler ve bakiyeli adresler, Gas tahmini, yönetici onaylı toplama |
| Günlük çalışma alanı | Çince ve İngilizce arayüz, açık ve koyu tema, mobil düzen, ayarlanabilir kenar çubuğu, ülkeye göre erişim kuralları, yedekleme / geri yükleme ve sürüm yönetimi |

### Üyelerin hangi düğümleri kullanacağını paket belirler

Yönetici bir paket için yerel, alt site veya herkese açık düğüm gruplarını ya da bir grup içindeki belirli düğümleri seçer. Üyeler mevcut paketlerini, kalan süre ve kotalarını, abonelik girişini ve istemcileri görür; düğüm gruplarını veya karma aboneliklerin iç kurallarını anlamaları gerekmez.

Başka bir paket satın alındığında mevcut paketin kalan geçerliliği durdurulur ve hak kuyruğuna göre yeniden başlar. İadeler yönetici onayı gerektirir: etkin paket kalan kota veya süreye göre iade edilir, kuyruktaki siparişler şu anda orijinal sipariş tutarını geri verir ve diğer hakların geri yüklenme sırası korunur. Kesin kurallar için [paketler](docs/PLANS.md) ve [siparişler ve iadeler](docs/COMMERCE_EN.md) belgelerine bakın. Etkin paketin düğüm, çarpan ve protokol değişiklikleri sunucu tarafı yetkilere eşitlenir; kota, geçerlilik süresi ve kuyruktaki paketler anlık görüntülerini korur. İstemcinin önbelleğe aldığı aboneliklerin yine de yenilenmesi gerekir.

### Alt site düğümleri ve alt site IP'leri iki ayrı yoldur

- **Bağlanan alt site düğümleri:** istemci doğrudan alt site düğümüne bağlanır; trafik ana siteden geçmez.
- **Alt site IP çıkışları:** istemci ana sitenin bir düğümüne bağlanır, ana site trafiği alt sitenin IP çıkışı üzerinden aktarır.

İki kaynak türü ayrı yönetilir ve kaynağına göre etiketlenir. Bir alt sitenin ortak bütçesi tükendiğinde erişim yapılandırmaya göre duraklatılır ve bekleme süresi bitince yeniden açılır; düğüm grubu üyelikleri korunur. Yapılandırma adımları için [düğüm bağlama ve bütçeler](docs/NODE_MOUNTS.md) belgesine bakın.

### Kripto tahsilat ve cüzdanlar

Kullanıcı kripto parayla ödemeyi seçtiğinde siparişe kalıcı olarak saklanan ayrı bir adres atanır. Otomatik tahsilat ve toplama şu anda Ethereum, BNB Smart Chain, Plasma ve Solana üzerindeki desteklenen USDT / USDC varlıklarını, sözleşme veya mint izin listesine göre kapsar; bir L2 yapılandırılmış olması, onun için otomatik mutabakat ve toplamanın desteklendiği anlamına gelmez.

Bakiye ve Gas sorguları salt okunurdur. **Ücretler yalnızca yönetici bir toplama işlemini onayladığında ödenir.** Otomatik ücret desteği yalnızca, seçilen izinli stablecoin bakiyesi 0,1'den kesin olarak büyük olan gerçek sipariş adreslerinde uygulanır. EVM'de yalnızca eksik kalan tutar tamamlanır; Solana'da ücretler sabit bir SOL fon adresinden ödenir. Başta onaylanan ücret üst sınırı aşılacaksa işlem elle inceleme için duraklatılır. Ayrıntılar ve zincirler arasındaki farklar için [cüzdan yönetimi](docs/CRYPTO_WALLETS.md) ve [sipariş tahsilatı ve toplama](docs/CRYPTO_TREASURY.md) belgelerine bakın.

Yerleşik sıcak cüzdan küçük işletme bakiyeleri içindir. Anımsatıcı ifade şifrelenmiş olarak saklansa da imzalama yetkisi aynı sunucuda kalır: özel bir yedek alın ve fonları düzenli olarak çekin. Harici bir EVM xpub adres türetme ve sorgulama için kullanılabilir; transferler harici cüzdandan yapılır ve sunucuya imzalama yetkisi vermez.

## Kapsam ve sınırlar

- Doğrudan sunulan giriş protokolleri **VLESS Reality/Vision ve Hysteria 2**'dir. İçe aktarılabilen kaynak biçimleri ayrıştırıcıya ve Mihomo'ya bağlıdır; aynı adlı yerel girişler eklemez.
- Kurulum şu anda site başına tek bir denetleyicidir. Aynı site için çok ana sunuculu yüksek erişilebilirlik, kuruluş düzeyinde kiracı yalıtımı, SLA veya bağımsız güvenlik denetimi vaat edilmez. Pro'nun veritabanı ve önbelleği, kendisi tıkalı olan bir ağ hattını iyileştirmez.
- CI; yalıtılmış Linux kurulumu, güncelleme kurtarma ve protokol doğrulamasını içerir. Bu, büyük ölçekli bir üretim kapasitesi raporu ya da ticari hesabınızın veya ana ağ işlemlerinizin kabulü değildir. Yayına almadan önce gerçek istemcileri, yedekleri, e-postayı ve ödeme akışlarını test edin.
- RPC, piyasa fiyatı ve kalite kontrol kaynakları sınırlanabilir veya kullanılamaz olabilir. Bilinmeyen sonuçlar hiçbir zaman sıfır ücret sayılmaz; herkese açık bir sayfaya ulaşmak hesapların, oynatmanın, ödemelerin veya bölgesel hizmetlerin açık olduğu anlamına gelmez.
- Bu depo **7 dilde proje tanıtımı** sunar; panel arayüzü şu anda **Basitleştirilmiş Çince ve İngilizceyi** destekler. Ayrıntılı kılavuzlar çoğunlukla Çincedir, bir kısmının İngilizcesi vardır; çevrilmiş bir tanıtım, arayüzün ve kılavuzların o dile yerelleştirildiği anlamına gelmez.

## Göreve göre belgeler

| Ne yapmak istiyorsunuz? | Buradan başlayın |
| --- | --- |
| Kurmak ve sürüm seçmek | [Tek adımda kurulum](docs/INSTALL.md) · [Docker](docs/DOCKER.md) · [Lite / Pro](docs/EDITIONS.md) |
| Paketleri, satışları ve üye aboneliklerini yapılandırmak | [Kullanım kılavuzu](docs/USER_GUIDE.md) · [Paketler](docs/PLANS.md) · [Siparişler ve ödemeler](docs/COMMERCE_EN.md) |
| Alt site bağlamak, düğüm bağlamak ve çıkış atamak | [Alt site yönetimi](docs/BUSINESS-SITES.md) · [Düğüm bağlama](docs/NODE_MOUNTS.md) · [Canlı izleme](docs/LIVE-MONITORING.md) |
| E-posta, kripto tahsilat ve toplamayı yapılandırmak | [E-posta](docs/EMAIL.md) · [Cüzdanlar](docs/CRYPTO_WALLETS.md) · [Tahsilat ve toplama](docs/CRYPTO_TREASURY.md) |
| İşletim, sorun giderme ve güncelleme | [İşletim](docs/OPERATIONS.md) · [Güncellemeler](docs/UPDATES.md) · [Sistem yapılandırması](docs/CONFIGURATION.md) |
| Geliştirme ve güvenlik bildirimi | [Katkı kılavuzu](CONTRIBUTING.md) · [Mimari](docs/ARCHITECTURE.md) · [Güvenlik](SECURITY.md) |

Tam dizin için [belge rehberine](docs/README.md) bakın. Tasarım kaynakları [Penpot tasarım kaynağı](docs/designs/penpot/README.md) ve [arayüz tasarım sistemi](docs/designs/penpot-courtyard-system.md) içindedir.

## Açık kaynak ve katkı

Yeniden üretilebilir sorun bildirimleri, öneriler ve pull request'ler memnuniyetle karşılanır. Sorun bildirirken sürümü, Lite / Pro bilgisini, kurulum yöntemini, site rolünü ve yeniden üretme adımlarını belirtin; gerçek abonelik bilgilerini, özel anahtarları, anımsatıcı ifadeleri veya üretim yapılandırmasının tamamını yüklemeyin. Kod ve belge değişikliklerinin doğrulama gereksinimleri [katkı kılavuzunda](CONTRIBUTING.md) yer alır; güvenlik sorunları için [özel bildirim sürecini](SECURITY.md) kullanın.

Guangyue Panel, 0.15.0 sürümünden beri **LGPL-3.0-only** lisanslıdır. fake-ui'nin orijinal MIT bildirimi [eski lisans](licenses/guangyue-legacy-MIT.txt) dosyasında, [GPL-3.0 metni](licenses/GPL-3.0.txt) ile birlikte korunur. Özel Hysteria yaması MIT, Xray MPL-2.0, Mihomo ise GPL-3.0 lisanslıdır; bileşen sahipliği ve karşılık gelen kaynak kodu için [üçüncü taraf bildirimlerine](THIRD_PARTY_NOTICES.md) bakın.

Bu tanıtımın yapısı [3x-ui](https://github.com/MHSanaei/3x-ui) örneğini izler ve çok dilli içerik Guangyue'nun gerçek yeteneklerine göre yazılmıştır; önceki belge yapısı Sub2API'den esinlenmiştir, ancak markası veya ekran görüntüleri kullanılmamıştır. Tam sürümler çalışma paketlerini, kurulum aracını, Docker imaj arşivlerini, sağlama toplamı listesini ve bağımlılık bilgilerini içerir; ayrıntılar için [sürüm notlarına](docs/RELEASE.md) bakın. Proje işinize yaradıysa bir yıldız verebilir veya gerçek bir kurulumdan geri bildirim paylaşabilirsiniz.
