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
deploy:
	$(GCLOUD) functions deploy invitechan \
		--env-vars-file $(env_file) \
		$(project_args) \
		--runtime go111 \
		--entry-point Do \
		--trigger-http
