package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"text/template"
)

func buildImage(version, build string) {
	output, err := exec.Command("docker", "image", "ls").Output()
	if err != nil {
		log.Fatalf("could not list docker images: %s", err)
	}

	tempFile, err := os.CreateTemp("/tmp/", "docker.file.*")
	if err != nil {
		log.Fatalf("could not create temp file: %s", err)
	}
	defer os.Remove(tempFile.Name())

	if !strings.Contains(string(output), "couchbase/server") || !strings.Contains(string(output), fmt.Sprintf("%s-%s", version, build)) {
		writeDockerfile(version, build, tempFile)
		cmd := exec.Command("docker", "build", ".", "-f", tempFile.Name(), "-t", fmt.Sprintf("couchbase/server:%s-%s", version, build))
		stderr, err := cmd.StderrPipe()
		if err != nil {
			log.Fatalf("could not build docker image: %s", err)
		}

		if err := cmd.Start(); err != nil {
			log.Fatalf("could not build docker image: %s", err)
		}

		errMessage, err := io.ReadAll(stderr)
		if err != nil {
			log.Fatalf("could not build docker image: %s", err)
		}
		log.Println(string(errMessage))

		if err := cmd.Wait(); err != nil {
			log.Fatalf("could not build docker image: %s", err)
		}

	}
}

func writeDockerfile(version, buildNum string, file *os.File) {
	vars := struct {
		Package string
		URL     string
	}{
		Package: fmt.Sprintf("couchbase-server-enterprise_%s-%s-ubuntu20.04_amd64.deb", version, buildNum),
		URL:     fmt.Sprintf("http://builds.com/%s/%s", version, buildNum),
	}

	const dockerFile = `
FROM ubuntu:20.04

ENV PATH=$PATH:/opt/couchbase/bin:/opt/couchbase/bin/tools:/opt/couchbase/bin/install

RUN set -x && \
    apt-get update && \
    apt-get install -yq runit wget chrpath tzdata \
    lsof lshw sysstat net-tools numactl bzip2 libtinfo5 && \
    apt-get autoremove && apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

RUN if [ ! -x /usr/sbin/runsvdir-start ]; then \
        cp -a /etc/runit/2 /usr/sbin/runsvdir-start; \
    fi

RUN groupadd -g 1000 couchbase && useradd couchbase -u 1000 -g couchbase -M

RUN set -x && \
    export INSTALL_DONT_START_SERVER=1 && \
    wget {{.URL}}/{{.Package}} && \
    apt install -y ./{{.Package}} && \
    rm -f ./{{.Package}} && \
    apt-get autoremove && apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

RUN mkdir -p /etc/service/couchbase-server/

RUN printf "#!/bin/sh \n\
exec 2>&1 \n\
cd /opt/couchbase \n\
mkdir -p var/lib/couchbase \n\
var/lib/couchbase/config \n\
var/lib/couchbase/data \n\
var/lib/couchbase/stats \n\
var/lib/couchbase/logs \n\
var/lib/moxi \n\
\n\
chown -R couchbase:couchbase var \n\
if [ \"\$(whoami)\" = \"couchbase\" ]; then \n\
exec /opt/couchbase/bin/couchbase-server -- -kernel global_enable_tracing false -noinput \n\
else \n\
exec chpst -ucouchbase  /opt/couchbase/bin/couchbase-server -- -kernel global_enable_tracing false -noinput \n\
fi" > /etc/service/couchbase-server/run

RUN chmod a+x /etc/service/couchbase-server/run
RUN chown -R couchbase:couchbase /etc/service

RUN mkdir -p /etc/runit/runsvdir/default/couchbase-server/supervise \
    && chown -R couchbase:couchbase \
                /etc/service \
                /etc/runit/runsvdir/default/couchbase-server/supervise

RUN printf "#!/bin/sh \
echo \"Running in Docker container - $0 not available\"" > /usr/local/bin/dummy.sh

RUN chmod a+x /usr/local/bin/dummy.sh

RUN ln -s dummy.sh /usr/local/bin/iptables-save && \
    ln -s dummy.sh /usr/local/bin/lvdisplay && \
    ln -s dummy.sh /usr/local/bin/vgdisplay && \
    ln -s dummy.sh /usr/local/bin/pvdisplay 

RUN printf "#!/bin/bash \n\
set -e \n\
\
staticConfigFile=/opt/couchbase/etc/couchbase/static_config \n\
restPortValue=8091 \n\
function overridePort() { \n\
portName=\$1 \n\
portNameUpper=\$(echo \$portName | awk '{print toupper(\$0)}') \n\
portValue=\${!portNameUpper} \n\
if [ \"\$portValue\" != \"\" ]; then \n\
if grep -Fq \"{\${portName},\" \${staticConfigFile} \n\
then \n\
echo \"Don't override port \${portName} because already available in \$staticConfigFile\" \n\
else \
echo \"Override port '\$portName' with value '\$portValue'\" \n\
echo \"{\$portName, \$portValue}.\" >> \${staticConfigFile} \n\
\
if [ \${portName} == \"rest_port\" ]; then \n\
restPortValue=\${portValue} \n\
fi \n\
fi \n\
fi \n\
} \n\
\n\
overridePort \"rest_port\" \n\
overridePort \"mccouch_port\" \n\
overridePort \"memcached_port\" \n\
overridePort \"query_port\" \n\
overridePort \"ssl_query_port\" \n\
overridePort \"fts_http_port\" \n\
overridePort \"moxi_port\" \n\
overridePort \"ssl_rest_port\" \n\
overridePort \"ssl_capi_port\" \n\
overridePort \"ssl_proxy_downstream_port\" \n\
overridePort \"ssl_proxy_upstream_port\" \n\
\n\
[[ \"\$1\" == \"couchbase-server\" ]] && { \n\
\n\
if [ \"\$(whoami)\" = \"couchbase\" ]; then \n\
\n\
if [ ! -w /opt/couchbase/var -o \n\
\$(find /opt/couchbase/var -maxdepth 0 -printf '%%u') != \"couchbase\" ]; then \n\
echo \"/opt/couchbase/var is not owned and writable by UID 1000\" \n\
echo \"Aborting as Couchbase Server will likely not run\" \n\
exit 1 \n\
fi \n\
fi \n\
echo \"Starting Couchbase Server -- Web UI available at http://ip:\$restPortValue\" \n\
echo \"and logs available in /opt/couchbase/var/lib/couchbase/logs\" \n\
exec /usr/sbin/runsvdir-start \n\ 
} \n\
\n\
exec \"\$@\" "> /entrypoint.sh

RUN chmod a+x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
CMD ["couchbase-server"]
`

	tmpl, err := template.New("internal").Parse(dockerFile)
	if err != nil {
		log.Fatalf("could not create template: %s", err)
	}

	err = tmpl.Execute(file, vars)
	if err != nil {
		log.Fatalf("could not execute template: %s", err)
	}

}
