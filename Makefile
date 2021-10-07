GCLOUD = gcloud

.PHONY: deploy
deploy:
	$(GCLOUD) functions deploy invitechan \
		--region=asia-northeast1 \
		--env-vars-file env.$(PROJECT).yaml \
		--project $(PROJECT) \
		--runtime go113 \
		--entry-point Do \
		--trigger-http

local:
	npx dotenv-cli -c invitechan-public -- go run ./cmd/local
	npx localtunnel --port 3000 --subdomain invitechan-dev
