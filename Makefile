GCLOUD = gcloud

project_args =
ifneq ($(PROJECT),)
    project_args = --project $(PROJECT)
endif

env_file = env.yaml
ifneq ($(ENV),)
    env_file = env.$(ENV).yaml
endif

.PHONY: deploy
deploy: deploy-command deploy-invitechan

.PHONY: deploy-command
deploy-command:
	$(GCLOUD) beta functions deploy command \
		--env-vars-file $(env_file) \
		$(project_args) \
		--region asia-northeast1 \
		--runtime go111 \
		--entry-point Command \
		--trigger-http \
		--allow-unauthenticated

.PHONY: deploy-invitechan
deploy-invitechan:
	$(GCLOUD) beta functions deploy invitechan \
		--env-vars-file $(env_file) \
		$(project_args) \
		--region asia-northeast1 \
		--runtime go111 \
		--entry-point Do \
		--trigger-http \
		--allow-unauthenticated

.PHONY: deploy-auth
deploy-auth:
	$(GCLOUD) functions deploy auth \
		--env-vars-file $(env_file) \
		$(project_args) \
		--runtime go111 \
		--entry-point Auth \
		--trigger-http
	$(GCLOUD) functions deploy auth-callback \
		--env-vars-file $(env_file) \
		$(project_args) \
		--runtime go111 \
		--entry-point AuthCallback \
		--trigger-http
