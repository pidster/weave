package proxy

import (
	"net/http"

	"github.com/fsouza/go-dockerclient"
)

type startContainerInterceptor struct{ proxy *Proxy }

type startContainerRequestBody struct {
	HostConfig *docker.HostConfig `json:"HostConfig,omitempty" yaml:"HostConfig,omitempty"`
}

func (i *startContainerInterceptor) InterceptRequest(r *http.Request) error {
	container, err := inspectContainerInPath(i.proxy.client, r.URL.Path)
	if err != nil {
		return err
	}

	// If the client has sent some JSON which might be a HostConfig, add our
	// parameters back into it, otherwise Docker will consider them overwritten
	if containerShouldAttach(container) && r.Header.Get("Content-Type") == "application/json" {
		params := map[string]interface{}{}
		if err := unmarshalRequestBody(r, &params); err != nil {
			return err
		}
		// HostConfig can be sent either unnamed at top level or as a struct named HostConfig
		hostConfig := params
		if subParam, found := params["HostConfig"]; found {
			if typecast, ok := subParam.(map[string]interface{}); ok {
				hostConfig = typecast
			}
		}
		if len(hostConfig) > 0 {
			i.proxy.addWeaveWaitVolume(hostConfig)
			if dnsDomain := i.proxy.getDNSDomain(); dnsDomain != "" {
				if err := i.proxy.setWeaveDNS(hostConfig, container.Config.Hostname, dnsDomain); err != nil {
					return err
				}
			}

			// Note we marshal the original top-level dictionary to avoid disturbing anything else
			if err := marshalRequestBody(r, params); err != nil {
				return err
			}
		}
	}
	i.proxy.createWait(r, container.ID)
	return nil
}

func (i *startContainerInterceptor) InterceptResponse(r *http.Response) error {
	defer i.proxy.removeWait(r.Request)
	if r.StatusCode < 200 || r.StatusCode >= 300 { // Docker didn't do the start
		return nil
	}
	i.proxy.waitForStart(r.Request)
	return nil
}
