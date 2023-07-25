set -e
apk add nodejs npm
make resolve
make build-linux
cp ./bin/kyma-linux /usr/local/bin/kyma
kyma provision k3d --ci
echo "BEFORE--/root/.kyma/sources/main"
ls /root/.kyma/sources/main
echo "BEFORE--cat"
cat $HOME/.kyma/sources/installation/resources/components.yaml
kyma deploy --ci
make -C "../kyma/tests/fast-integration" "ci"
echo "AFTER--/root/.kyma/sources/main"
ls /root/.kyma/sources/main
echo "AFTER--cat"
cat $HOME/.kyma/sources/installation/resources/components.yaml
kyma undeploy --ci --timeout=10m0s
