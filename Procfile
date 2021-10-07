server: DATASTORE_EMULATOR_HOST=0.0.0.0:8081 INVITECHAN_AUTH_REDIRECT_URL=https://invitechan-dev.loca.lt/auth/callback go run ./cmd/local
localtunnel: npx localtunnel --port 8810 --subdomain invitechan-dev
# datastore: docker run --rm -i -e CLOUDSDK_CORE_PROJECT=$GCP_PROJECT -p 8081:8081 motemen/datastore-emulator
