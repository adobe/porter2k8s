#!/bin/bash -e
go get -u golang.org/x/lint/golint
echo Linting
golint -set_exit_status .
echo "Looking for go files with >120 character lines."
LINES=$(grep -rn '.\{121\}' *.go)
if [[ -n $LINES ]]; then
  echo $LINES
  exit 1
fi
echo Passed
