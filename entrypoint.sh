#!/usr/bin/env sh

if [ -z ${JIRA_USER} ]; then
	echo "JIRA_USER is mandatory environment variable"
	exit 1
fi

if [ -z ${JIRA_PASS} ]; then
        echo "JIRA_PASS is mandatory environment variable"
        exit 1
fi

exec $@
