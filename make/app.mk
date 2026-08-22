# Application Build and Deployment Targets
# ### each app provides its own configuration (apps/<name>/app.env) containing
# BUILD_STRATEGY, DEPLOY_STRATEGY and any strategy-specific variables.  make
# targets simply load that file and invoke the corresponding script.

# Apps are the sub-directories of apps/ only. Enumerate directories, not files,
# so loose files like README.md or app.env.example are never mistaken for an
# app (which made `destroy-all-apps`/`deploy-all` try to act on `README.md`).
#
# `filter %/` keeps only the trailing-slash (directory) entries: GNU make 3.81
# that Apple ships returns files from `wildcard apps/*/` too, but only real
# directories carry the trailing slash — so this is correct on make 3.81 and 4.x.
APPS := $(notdir $(patsubst %/,%,$(filter %/,$(wildcard apps/*/))))


.PHONY: build local-run deploy destroy-app lint validate \
        deploy-all destroy-all-apps

# build target dispatches to the chosen build strategy
build:
	@bash engine/build.sh $(APP_NAME)

# run locally if the app is a binary
local-run:
	@bash engine/run.sh $(APP_NAME)

# deploy/destroy are completely strategy-driven
deploy:
	@bash engine/deploy.sh deploy $(APP_NAME)

destroy-app:
	@bash engine/deploy.sh destroy $(APP_NAME)

lint:
	@bash engine/deploy.sh lint $(APP_NAME)

validate:
	@bash engine/deploy.sh validate $(APP_NAME)

# Bulk operations for all apps
deploy-all: $(APPS:%=deploy-%)

$(APPS:%=deploy-%):
	@$(MAKE) deploy APP_NAME=$(@:deploy-%=%)

destroy-all-apps: $(APPS:%=destroy-%)

$(APPS:%=destroy-%):
	@$(MAKE) destroy-app APP_NAME=$(@:destroy-%=%)