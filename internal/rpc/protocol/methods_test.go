package protocol

import "testing"

func TestCategoryOfMethod(t *testing.T) {
	t.Parallel()

	cases := []struct {
		method string
		want   MethodCategory
	}{
		{method: MethodPing, want: CategorySystem},
		{method: MethodSessionAttach, want: CategorySession},
		{method: MethodSessionDetach, want: CategorySession},
		{method: MethodWorkflowSubmit, want: CategoryWorkflow},
		{method: MethodWorkflowCancel, want: CategoryWorkflow},
		{method: MethodWorkflowGet, want: CategoryWorkflow},
		{method: MethodWorkflowList, want: CategoryWorkflow},
		{method: MethodWorkflowObjects, want: CategoryWorkflow},
		{method: MethodEventSubscribe, want: CategoryEvent},
		{method: MethodEventUnsubscribe, want: CategoryEvent},
		{method: MethodEventPush, want: CategoryEvent},
		{method: MethodMessageList, want: CategoryMessage},
		{method: MethodCheckpointGet, want: CategoryCheckpoint},
		{method: MethodCheckpointList, want: CategoryCheckpoint},
		{method: MethodWorkspaceChanges, want: CategoryWorkspace},
		{method: MethodWorkerDescribe, want: CategoryWorker},
		{method: MethodWorkerExecute, want: CategoryWorker},
		{method: MethodWorkerCancel, want: CategoryWorker},
		{method: MethodToolDescribe, want: CategoryTool},
		{method: MethodToolExec, want: CategoryTool},
		{method: MethodToolCancel, want: CategoryTool},
		{method: "unknown.method", want: CategoryUnknown},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()
			if got := CategoryOf(tc.method); got != tc.want {
				t.Fatalf("CategoryOf(%q) = %q, want %q", tc.method, got, tc.want)
			}
		})
	}
}

func TestPlaneOfMethod(t *testing.T) {
	t.Parallel()

	cases := []struct {
		method string
		want   MethodPlane
	}{
		{method: MethodPing, want: PlaneShared},
		{method: MethodSessionAttach, want: PlaneClientToKernel},
		{method: MethodWorkflowSubmit, want: PlaneClientToKernel},
		{method: MethodWorkflowGet, want: PlaneClientToKernel},
		{method: MethodWorkflowList, want: PlaneClientToKernel},
		{method: MethodWorkflowObjects, want: PlaneClientToKernel},
		{method: MethodMessageList, want: PlaneClientToKernel},
		{method: MethodWorkspaceChanges, want: PlaneClientToKernel},
		{method: MethodWorkerDescribe, want: PlaneKernelToWorker},
		{method: MethodWorkerCancel, want: PlaneKernelToWorker},
		{method: MethodToolDescribe, want: PlaneKernelToTool},
		{method: MethodToolCancel, want: PlaneKernelToTool},
		{method: MethodEventPush, want: PlaneServerPush},
		{method: "unknown.method", want: PlaneUnknown},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()
			if got := PlaneOf(tc.method); got != tc.want {
				t.Fatalf("PlaneOf(%q) = %q, want %q", tc.method, got, tc.want)
			}
		})
	}
}

func TestMethodSupportByServerType(t *testing.T) {
	t.Parallel()

	if !SupportsKernelServer(MethodWorkflowSubmit) {
		t.Fatal("kernel server should support workflow.submit")
	}
	if !SupportsKernelServer(MethodWorkflowGet) {
		t.Fatal("kernel server should support workflow.get")
	}
	if SupportsKernelServer(MethodWorkerDescribe) {
		t.Fatal("kernel server must not expose worker.describe")
	}
	if !SupportsWorkerServer(MethodWorkerExecute) {
		t.Fatal("worker server should support worker.execute")
	}
	if SupportsWorkerServer(MethodToolExec) {
		t.Fatal("worker server must not expose tool.exec")
	}
	if !SupportsToolServer(MethodToolExec) {
		t.Fatal("tool server should support tool.exec")
	}
	if SupportsToolServer(MethodEventSubscribe) {
		t.Fatal("tool server must not expose event.subscribe")
	}
}
