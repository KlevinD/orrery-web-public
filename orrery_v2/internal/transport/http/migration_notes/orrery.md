# Orrery HTTP Route Migration Notes

Stage 10 keeps useful route shape and request parsing experience from the legacy handlers, but no handler may directly read repositories, SQL queries, runtime adapters, queues, or event stores.

| Legacy source | Stage 10 route | Disposition | Contract evidence |
| --- | --- | --- | --- |
| `backend/internal/handler/mission.go` | `GET /api/missions/{mission_id}/control`, `GET /api/missions/{mission_id}/team`, `GET /api/missions/{mission_id}/tasks`, `GET /api/missions/{mission_id}/progress`, `GET /api/missions/{mission_id}/evidence`, `GET /api/missions/{mission_id}/qa`, `GET /api/missions/{mission_id}/discussion` | Renamed from mission detail/list shape to MissionControl app query DTO and permission-trimmed subviews. | `mission_control_handler_test.go`, `mission_view_routes_test.go` |
| Mission creation/list legacy handlers | `GET /api/missions`, `POST /api/missions` | Implemented as app-level catalog/create ports; transport does not construct mission domain records. | `mission_lifecycle_routes_test.go`, `mission_task_ports_test.go` |
| `backend/internal/handler/pm_tools_mission_task_http.go` | `POST /api/missions/{mission_id}/tasks/{task_id}/submit-result` | PM tool semantics move behind `missioncontrol.CommandService`. | `task_handler_test.go`, `command_service_test.go` |
| Task creation/assignment handlers | `POST /api/missions/{mission_id}/tasks`, `POST /api/missions/{mission_id}/tasks/{task_id}/assign` | Implemented as app-level task command ports; assignment policy remains outside transport. | `mission_lifecycle_routes_test.go`, `mission_task_ports_test.go` |
| `backend/internal/handler/comment.go` | `POST /api/missions/{mission_id}/discussion/messages`, `POST /api/missions/{mission_id}/discussion/messages/{message_id}/reply` | Implemented through `missioncontrol.CommandService`; legacy issue-centered fields are not retained. | `mission_command_routes_test.go`, `command_routes_test.go` |
| `backend/internal/handler/inbox.go` | `GET /api/inbox`, `POST /api/inbox/{item_id}/read`, `POST /api/inbox/{item_id}/close` | Implemented as Mission Discussion Inbox API surface through app service ports. | `mission_command_routes_test.go`, `command_routes_test.go` |
| `backend/internal/handler/qa.go` | `POST /api/missions/{mission_id}/qa/{qa_id}/decision` | Implemented as app command route; QA rules stay in task delivery / QA app service. | `mission_command_routes_test.go`, `command_routes_test.go` |
