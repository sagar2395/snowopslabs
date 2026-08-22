# Services targets — manage shared services (redis, postgres, etc.)
# Each service lives under services/<name>/ and has install.sh, uninstall.sh, status.sh

SERVICES_DIR := services

.PHONY: service-list service-up service-down service-status

service-list:
	@echo "Available shared services:"
	@for dir in $(SERVICES_DIR)/*/; do \
		if [ -f "$$dir/install.sh" ]; then \
			echo "  - $$(basename $$dir)"; \
		fi \
	done

service-up:
	@if [ -z "$(SERVICE)" ]; then echo "Usage: make service-up SERVICE=<name>"; exit 1; fi
	@echo "Installing service $(SERVICE)..."
	@bash $(SERVICES_DIR)/$(SERVICE)/install.sh

service-down:
	@if [ -z "$(SERVICE)" ]; then echo "Usage: make service-down SERVICE=<name>"; exit 1; fi
	@echo "Uninstalling service $(SERVICE)..."
	@bash $(SERVICES_DIR)/$(SERVICE)/uninstall.sh

service-status:
	@if [ -n "$(SERVICE)" ]; then \
		bash $(SERVICES_DIR)/$(SERVICE)/status.sh; \
	else \
		for dir in $(SERVICES_DIR)/*/; do \
			if [ -f "$$dir/status.sh" ]; then \
				echo "--- $$(basename $$dir) ---"; \
				bash "$$dir/status.sh" || true; \
				echo ""; \
			fi \
		done \
	fi
