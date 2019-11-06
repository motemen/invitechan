GCLOUD = gcloud

project_args =
ifneq ($(PROJECT),)
    project_args = --project $(PROJECT)
endif

.PHONY: deploy
deploy: deploy-command deploy-invitechan

.PHONY: deploy-command
deploy-command:
	$(GCLOUD) beta functions deploy command \
		--env-vars-file env.yaml \
		$(project_args) \
		--region asia-northeast1 \
		--runtime go111 \
		--entry-point Command \
		--trigger-http \
		--allow-unauthenticated

.PHONY: deploy-invitechan
deploy-invitechan:
	$(GCLOUD) beta functions deploy invitechan \
		--env-vars-file env.yaml \
		$(project_args) \
		--region asia-northeast1 \
		--runtime go111 \
		--entry-point Do \
		--trigger-http \
		--allow-unauthenticated
