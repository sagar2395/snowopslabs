# Documentation gates. Documentation is the source of truth, so it is checked
# like code is.

.PHONY: docs-check docs-links docs-manifest

## docs-check: every documentation gate
docs-check: docs-links docs-manifest

## docs-links: no relative link in the docs points at a missing file
docs-links:
	@sh scripts/docs-links.sh

## docs-manifest: every file the website publishes still exists
docs-manifest:
	@sh scripts/docs-manifest-check.sh
