GCLOUD = gcloud

.PHONY: deploy
deploy:
	$(GCLOUD) beta functions deploy invitechan \
		--env-vars-file env.yaml \
		--project $(PROJECT) \
		--region asia-northeast1 \
		--runtime go111 \
		--entry-point Do \
		--trigger-http \
		--allow-unauthenticated
