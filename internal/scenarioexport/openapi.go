package scenarioexport

import (
	"errors"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/yamlx"
)

func (s *Service) renderOpenAPI(contract designscenario.Contract, format Format) ([]byte, error) {
	if s.maxBytes <= 0 || int64(len(contract.Document)) > s.maxBytes {
		return nil, ErrTooLarge
	}
	if format == OpenAPIJSON {
		return contract.Document, nil
	}
	out, err := yamlx.FromJSONLimit(contract.Document, s.maxBytes)
	if errors.Is(err, yamlx.ErrOutputTooLarge) {
		return nil, ErrTooLarge
	}
	return out, err
}
