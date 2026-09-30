package handlers

import (
	"context"
	"testing"

	"github.com/artyomsv/marauder/backend/internal/domain"
	"github.com/artyomsv/marauder/backend/internal/plugins/registry"
)

type fakeInfoClient struct{ name string }

func (f *fakeInfoClient) Name() string                       { return f.name }
func (f *fakeInfoClient) DisplayName() string                { return f.name }
func (f *fakeInfoClient) ConfigSchema() map[string]any       { return nil }
func (f *fakeInfoClient) Test(context.Context, []byte) error { return nil }
func (f *fakeInfoClient) Add(context.Context, []byte, *domain.Payload, domain.AddOptions) error {
	return nil
}

type fakeSelectingClient struct{ fakeInfoClient }

func (f *fakeSelectingClient) Files(context.Context, []byte, string) ([]domain.ClientFile, error) {
	return nil, nil
}
func (f *fakeSelectingClient) SkipFiles(context.Context, []byte, string, []int) error { return nil }
func (f *fakeSelectingClient) Start(context.Context, []byte, string) error            { return nil }

func TestListClientInfos_SupportsFileSelection(t *testing.T) {
	infos := listClientInfos([]registry.Client{
		&fakeSelectingClient{fakeInfoClient{name: "selecting"}},
		&fakeInfoClient{name: "plain"},
	})
	if infos[0]["supports_file_selection"] != true {
		t.Errorf("selecting client: supports_file_selection = %v, want true", infos[0]["supports_file_selection"])
	}
	if infos[1]["supports_file_selection"] != false {
		t.Errorf("plain client: supports_file_selection = %v, want false", infos[1]["supports_file_selection"])
	}
	if infos[0]["name"] != "selecting" || infos[0]["display_name"] != "selecting" {
		t.Errorf("name fields = %v, want the plugin's names kept", infos[0])
	}
}
