// Copyright 2018 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"

	"github.com/golang/protobuf/ptypes/empty"
	api "github.com/kubeflow/pipelines/backend/api/go_client"
	"github.com/kubeflow/pipelines/backend/src/apiserver/common"
	"github.com/kubeflow/pipelines/backend/src/apiserver/model"
	"github.com/kubeflow/pipelines/backend/src/apiserver/resource"
	"github.com/kubeflow/pipelines/backend/src/common/util"
	"github.com/pkg/errors"
)

type RunServer struct {
	resourceManager *resource.ResourceManager
}

func (s *RunServer) CreateRun(ctx context.Context, request *api.CreateRunRequest) (*api.RunDetail, error) {
	err := s.validateCreateRunRequest(request)
	if err != nil {
		return nil, util.Wrap(err, "Validate create run request failed.")
	}
	err = CanAccessExperimentInResourceReferences(s.resourceManager, ctx, request.Run.ResourceReferences)
	if err != nil {
		return nil, util.Wrap(err, "Failed to authorize the request.")
	}

	run, err := s.resourceManager.CreateRun(request.Run)
	if err != nil {
		return nil, util.Wrap(err, "Failed to create a new run.")
	}
	return ToApiRunDetail(run), nil
}

func (s *RunServer) GetRun(ctx context.Context, request *api.GetRunRequest) (*api.RunDetail, error) {
	err := s.canAccessRun(ctx, request.RunId)
	if err != nil {
		return nil, util.Wrap(err, "Failed to authorize the request.")
	}
	run, err := s.resourceManager.GetRun(request.RunId)
	if err != nil {
		return nil, err
	}
	return ToApiRunDetail(run), nil
}

func (s *RunServer) ListRuns(ctx context.Context, request *api.ListRunsRequest) (*api.ListRunsResponse, error) {
	opts, err := validatedListOptions(&model.Run{}, request.PageToken, int(request.PageSize), request.SortBy, request.Filter)
	if err != nil {
		return nil, util.Wrap(err, "Failed to create list options")
	}

	filterContext, err := ValidateFilter(request.ResourceReferenceKey)
	if err != nil {
		return nil, util.Wrap(err, "Validating filter failed.")
	}

	if common.IsMultiUserMode() {
		refKey := filterContext.ReferenceKey
		if refKey == nil {
			return nil, util.NewInvalidInputError("ListRuns must filter by resource reference in multi-user mode.")
		}
		if refKey.Type == common.Namespace {
			namespace := refKey.ID
			if len(namespace) == 0 {
				return nil, util.NewInvalidInputError("Invalid resource references for ListRuns. Namespace is empty.")
			}
			err = isAuthorized(s.resourceManager, ctx, namespace)
			if err != nil {
				return nil, util.Wrap(err, "Failed to authorize with namespace resource reference.")
			}
		} else if refKey.Type == common.Experiment || refKey.Type == "ExperimentUUID" {
			// "ExperimentUUID" was introduced for perf optimization. We accept both refKey.Type for backward-compatible reason.
			experimentID := refKey.ID
			if len(experimentID) == 0 {
				return nil, util.NewInvalidInputError("Invalid resource references for run. Experiment ID is empty.")
			}
			err = CanAccessExperiment(s.resourceManager, ctx, experimentID)
			if err != nil {
				return nil, util.Wrap(err, "Failed to authorize with experiment resource reference.")
			}
		} else {
			return nil, util.NewInvalidInputError("Invalid resource references for ListRuns. Got %+v", request.ResourceReferenceKey)
		}
	}

	runs, total_size, nextPageToken, err := s.resourceManager.ListRuns(filterContext, opts)
	if err != nil {
		return nil, util.Wrap(err, "Failed to list runs.")
	}
	return &api.ListRunsResponse{Runs: ToApiRuns(runs), TotalSize: int32(total_size), NextPageToken: nextPageToken}, nil
}

func (s *RunServer) ArchiveRun(ctx context.Context, request *api.ArchiveRunRequest) (*empty.Empty, error) {
	err := s.canAccessRun(ctx, request.Id)
	if err != nil {
		return nil, util.Wrap(err, "Failed to authorize the request.")
	}
	err = s.resourceManager.ArchiveRun(request.Id)
	if err != nil {
		return nil, err
	}
	return &empty.Empty{}, nil
}

func (s *RunServer) UnarchiveRun(ctx context.Context, request *api.UnarchiveRunRequest) (*empty.Empty, error) {
	err := s.canAccessRun(ctx, request.Id)
	if err != nil {
		return nil, util.Wrap(err, "Failed to authorize the request.")
	}
	err = s.resourceManager.UnarchiveRun(request.Id)
	if err != nil {
		return nil, err
	}
	return &empty.Empty{}, nil
}

func (s *RunServer) DeleteRun(ctx context.Context, request *api.DeleteRunRequest) (*empty.Empty, error) {
	err := s.canAccessRun(ctx, request.Id)
	if err != nil {
		return nil, util.Wrap(err, "Failed to authorize the request.")
	}
	err = s.resourceManager.DeleteRun(request.Id)
	if err != nil {
		return nil, err
	}
	return &empty.Empty{}, nil
}

func (s *RunServer) ReportRunMetrics(ctx context.Context, request *api.ReportRunMetricsRequest) (*api.ReportRunMetricsResponse, error) {
	// Makes sure run exists
	_, err := s.resourceManager.GetRun(request.GetRunId())
	if err != nil {
		return nil, err
	}
	response := &api.ReportRunMetricsResponse{
		Results: []*api.ReportRunMetricsResponse_ReportRunMetricResult{},
	}
	for _, metric := range request.GetMetrics() {
		err := ValidateRunMetric(metric)
		if err == nil {
			err = s.resourceManager.ReportMetric(metric, request.GetRunId())
		}
		response.Results = append(
			response.Results,
			NewReportRunMetricResult(metric.GetName(), metric.GetNodeId(), err))
	}
	return response, nil
}

func (s *RunServer) ReadArtifact(ctx context.Context, request *api.ReadArtifactRequest) (*api.ReadArtifactResponse, error) {
	content, err := s.resourceManager.ReadArtifact(
		request.GetRunId(), request.GetNodeId(), request.GetArtifactName())
	if err != nil {
		return nil, util.Wrapf(err, "failed to read artifact '%+v'.", request)
	}
	return &api.ReadArtifactResponse{
		Data: content,
	}, nil
}

func (s *RunServer) validateCreateRunRequest(request *api.CreateRunRequest) error {
	run := request.Run
	if run.Name == "" {
		return util.NewInvalidInputError("The run name is empty. Please specify a valid name.")
	}

	if err := ValidatePipelineSpec(s.resourceManager, run.PipelineSpec); err != nil {
		if _, errResourceReference := CheckPipelineVersionReference(s.resourceManager, run.ResourceReferences); errResourceReference != nil {
			return util.Wrap(err, "Neither pipeline spec nor pipeline version is valid. "+errResourceReference.Error())
		}
		return nil
	}
	return nil
}

func (s *RunServer) TerminateRun(ctx context.Context, request *api.TerminateRunRequest) (*empty.Empty, error) {
	err := s.canAccessRun(ctx, request.RunId)
	if err != nil {
		return nil, util.Wrap(err, "Failed to authorize the request.")
	}
	err = s.resourceManager.TerminateRun(request.RunId)
	if err != nil {
		return nil, err
	}
	return &empty.Empty{}, nil
}

func (s *RunServer) RetryRun(ctx context.Context, request *api.RetryRunRequest) (*empty.Empty, error) {
	err := s.canAccessRun(ctx, request.RunId)
	if err != nil {
		return nil, util.Wrap(err, "Failed to authorize the request.")
	}
	err = s.resourceManager.RetryRun(request.RunId)
	if err != nil {
		return nil, err
	}
	return &empty.Empty{}, nil

}

func (s *RunServer) canAccessRun(ctx context.Context, runId string) error {
	if common.IsMultiUserMode() == false {
		// Skip authz if not multi-user mode.
		return nil
	}
	namespace, err := s.resourceManager.GetNamespaceFromRunID(runId)
	if err != nil {
		return util.Wrap(err, "Failed to authorize with the run Id.")
	}
	if len(namespace) == 0 {
		return util.NewInternalServerError(errors.New("There is no namespace found"), "There is no namespace found")
	}

	err = isAuthorized(s.resourceManager, ctx, namespace)
	if err != nil {
		return util.Wrap(err, "Failed to authorize with API resource references")
	}
	return nil
}

func NewRunServer(resourceManager *resource.ResourceManager) *RunServer {
	return &RunServer{resourceManager: resourceManager}
}
