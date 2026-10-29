{{- define "dra-inspect.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "dra-inspect.serviceAccountName" -}}
{{- default (include "dra-inspect.fullname" .) .Values.serviceAccount.name -}}
{{- end -}}

{{- define "dra-inspect.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
{{- end -}}

{{- define "dra-inspect.image" -}}
{{- $img := printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- if .Values.image.digest }}{{ $img = printf "%s@%s" $img .Values.image.digest }}{{ end -}}
{{- $img -}}
{{- end -}}

{{/* Namespaces that get a Role: the listed ones, else the release namespace. */}}
{{- define "dra-inspect.namespaces" -}}
{{- toJson (default (list .Release.Namespace) .Values.rbac.namespaces) -}}
{{- end -}}

{{/* get/list rules shared by both profiles; the cluster-only ones are added by clusterrole.yaml. */}}
{{- define "dra-inspect.kubevirtRule" -}}
{{- if .Values.rbac.kubevirt }}
- apiGroups: ["kubevirt.io"]
  resources: ["virtualmachineinstances"]
  verbs: ["get", "list"]
{{- end }}
{{- end -}}
