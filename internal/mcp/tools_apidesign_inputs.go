package mcp

type apiDesignIDInput struct {
	DesignID int64 `json:"designId" jsonschema:"project id from list_api_designs"`
}

type createAPIDesignInput struct {
	Name        string `json:"name"`
	Document    string `json:"document,omitempty" jsonschema:"full OpenAPI JSON or YAML text; exclusive with workspaceId"`
	WorkspaceID *int64 `json:"workspaceId,omitempty" jsonschema:"capture an existing workspace coherently; source stays unchanged"`
}

type saveAPIDesignInput struct {
	DesignID        int64  `json:"designId"`
	ExpectedVersion int64  `json:"expectedVersion" jsonschema:"exact design.version from your own get_api_design read; no blind conflict retries"`
	Document        string `json:"document" jsonschema:"COMPLETE authored OpenAPI JSON or YAML document; all unmentioned fields are removed"`
	Summary         string `json:"summary" jsonschema:"short explanation of this change for the analyst"`
	ChangeSetID     *int64 `json:"changeSetId,omitempty" jsonschema:"open task from create_api_design_change_set"`
}

type apiDesignRevisionInput struct {
	DesignID   int64 `json:"designId"`
	RevisionID int64 `json:"revisionId"`
}

type apiDesignDiffInput struct {
	DesignID       int64  `json:"designId"`
	FromRevisionID *int64 `json:"fromRevisionId,omitempty"`
	ToRevisionID   *int64 `json:"toRevisionId,omitempty"`
}

type validateAPIDesignInput struct {
	DesignID int64  `json:"designId"`
	Document string `json:"document"`
}

type createAPIDesignChangeSetInput struct {
	DesignID        int64  `json:"designId"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Title           string `json:"title"`
}

type closeAPIDesignChangeSetInput struct {
	DesignID        int64 `json:"designId"`
	ChangeSetID     int64 `json:"changeSetId"`
	ExpectedVersion int64 `json:"expectedVersion"`
}

type requestAPIDesignReviewInput struct {
	DesignID        int64  `json:"designId"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Summary         string `json:"summary"`
}

type restoreAPIDesignInput struct {
	DesignID        int64  `json:"designId"`
	ExpectedVersion int64  `json:"expectedVersion"`
	RevisionID      int64  `json:"revisionId"`
	Summary         string `json:"summary"`
}

type apiDesignVersionBody struct {
	ExpectedVersion int64 `json:"expectedVersion"`
}
