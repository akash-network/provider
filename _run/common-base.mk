include $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../make/init.mk)

AP_RUN_NAME := $(notdir $(CURDIR))
AKASH_HOME  ?= $(DEVCACHE_RUN)/$(AP_RUN_NAME)

.PHONY: bins
bins:
	make -C $(AP_ROOT) bins
