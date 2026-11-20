# GPUHub - GHCR imajını ZORLA yeniden çek ve container'ı yeniden oluştur
# CasaOS / herhangi bir Docker host'ta çalıştırın.

# 1) Mevcut eski container'ı durdur ve KALDIR (cache'den eski layer'lar kalmasın)
docker rm -f GPUHub 2>/dev/null || true

# 2) `latest` etiketini FORCE pull (digest doğrulaması için --platform belirtiyoruz)
#    Not: Eski imajları da temizlemek isterseniz: docker image rm ghcr.io/neighboth/gpuhub:latest 2>/dev/null || true
docker pull ghcr.io/neighboth/gpuhub:latest

# 3) Çekilen imajın digest'ini DOĞRULA (aşağıdaki değer CI'ın bastığı son digest olmalı)
docker images --digests ghcr.io/neighboth/gpuhub | head -3
#    Beklenen IMAGE ID / digest: sha256:4df3c8a80689b2fac85fb8b88d4edea42160727c41718956dbebd877f2d52b2d

# 4) Container'ı YENİDEN oluştur (deploy/casaos/docker-compose.local.yml içeriğiyle)
#    CasaOS kullanıyorsanız: App -> GPUHub -> "Stop" -> "Reinstall" (compose içeriği aynı)
#    veya SSH ile:
#    cd <compose dosyasının olduğu dizin>
#    docker compose up -d --force-recreate --pull always

# 5) İçerideki kodu DOĞRULA (en kesin test)
docker exec GPUHub sh -c 'grep -c "nativeBridgeResult" /app/gpuhub 2>/dev/null || strings /app/gpuhub | grep -c nativeBridgeResult'
#    0'dan büyük bir sayı dönmeli -> yeni binary (native fallback + AI test) yüklü demektir.

# 6) UI'da kontrol: sol menüde "AI Test" görünmeli, Providers->Test artık madde madde sonuç göstermeli.
