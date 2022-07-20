include $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../make/init.mk)

AP_RUN_NAME := $(notdir $(CURDIR))
AP_RUN_DIR  := $(DEVCACHE_RUN)/$(AP_RUN_NAME)
AKASH_HOME  ?= $(DEVCACHE_RUN)/$(AP_RUN_NAME)/.akash

.PHONY: bins
bins:
ifneq ($(SKIP_BUILD), true)
	make -C $(AP_ROOT) bins
endif
