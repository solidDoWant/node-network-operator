THIS_MAKEFILE_DIRECTORY = $(dir $(abspath $(lastword $(MAKEFILE_LIST))))

SCHEMAGEN_DIR = $(abspath $(THIS_MAKEFILE_DIRECTORY)/schema-gen)
OPENAPI2JSONSCHEMA = $(SCHEMAGEN_DIR)/openapi2jsonschema.py
OPENAPI2JSONSCHEMA_VERSION = 781c133dcebed26c2701701f5090aaf36991e7b8

CRD_DIR = $(abspath $(THIS_MAKEFILE_DIRECTORY)../config/crd/bases/)
CRD_FILES = $(shell find "$(CRD_DIR)" -name '*.yaml' -type f)

API_DIR = $(abspath $(THIS_MAKEFILE_DIRECTORY)../api/)
API_FILES = $(shell find "$(API_DIR)" -name '*_types.go' -type f)
SCHEMAS_DIR = $(abspath $(THIS_MAKEFILE_DIRECTORY)../schemas/)
SCHEMA_FILES = $(foreach f,$(API_FILES:$(API_DIR)/%_types.go=%),$(SCHEMAS_DIR)/$(notdir $(f))_$(subst /,,$(dir $(f))).json)

.PHONY: schemas
schemas: $(SCHEMA_FILES)

.PHONY: schema-gen-requirements
schema-gen-requirements: $(SCHEMAGEN_DIR)/.requirements-installed

$(SCHEMAGEN_DIR)/.requirements-installed:
	pip install --break-system-packages -r "$(SCHEMAGEN_DIR)/requirements.txt"
	touch $(@)

$(SCHEMAS_DIR):
	mkdir -p "$(SCHEMAS_DIR)"

$(OPENAPI2JSONSCHEMA): $(SCHEMAGEN_DIR)/.requirements-installed
	curl -fsSL -o "$(OPENAPI2JSONSCHEMA)" "https://raw.githubusercontent.com/datreeio/CRDs-catalog/$(OPENAPI2JSONSCHEMA_VERSION)/Utilities/openapi2jsonschema.py"

$(SCHEMA_FILES) &: $(SCHEMAS_DIR) $(OPENAPI2JSONSCHEMA)
	cd "$(SCHEMAS_DIR)" && python "$(OPENAPI2JSONSCHEMA)" $(CRD_FILES)

