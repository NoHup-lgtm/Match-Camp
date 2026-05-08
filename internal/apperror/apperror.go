package apperror

import "net/http"

type Definition struct {
	Code    string
	Message string
	Status  int
}

type Response struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

var definitions = map[string]Definition{
	"database_unavailable":            {Code: "database_unavailable", Message: "Banco de dados indisponivel.", Status: http.StatusServiceUnavailable},
	"redis_unavailable":               {Code: "redis_unavailable", Message: "Servico de tempo real indisponivel.", Status: http.StatusServiceUnavailable},
	"invalid_json":                    {Code: "invalid_json", Message: "JSON invalido.", Status: http.StatusBadRequest},
	"invalid_register_payload":        {Code: "invalid_register_payload", Message: "Dados de cadastro invalidos.", Status: http.StatusBadRequest},
	"email_domain_not_allowed":        {Code: "email_domain_not_allowed", Message: "Dominio de email nao permitido.", Status: http.StatusForbidden},
	"password_hash_failed":            {Code: "password_hash_failed", Message: "Falha ao proteger a senha.", Status: http.StatusInternalServerError},
	"email_already_registered":        {Code: "email_already_registered", Message: "Email ja cadastrado.", Status: http.StatusConflict},
	"register_failed":                 {Code: "register_failed", Message: "Falha ao criar cadastro.", Status: http.StatusInternalServerError},
	"session_create_failed":           {Code: "session_create_failed", Message: "Falha ao criar sessao.", Status: http.StatusInternalServerError},
	"invalid_credentials":             {Code: "invalid_credentials", Message: "Email ou senha invalidos.", Status: http.StatusUnauthorized},
	"login_failed":                    {Code: "login_failed", Message: "Falha ao realizar login.", Status: http.StatusInternalServerError},
	"google_oauth_not_configured":     {Code: "google_oauth_not_configured", Message: "Login com Google nao configurado.", Status: http.StatusServiceUnavailable},
	"oauth_state_failed":              {Code: "oauth_state_failed", Message: "Falha ao iniciar autenticacao externa.", Status: http.StatusInternalServerError},
	"invalid_oauth_state":             {Code: "invalid_oauth_state", Message: "Estado OAuth invalido.", Status: http.StatusBadRequest},
	"missing_oauth_code":              {Code: "missing_oauth_code", Message: "Codigo OAuth ausente.", Status: http.StatusBadRequest},
	"google_oauth_failed":             {Code: "google_oauth_failed", Message: "Falha ao autenticar com Google.", Status: http.StatusUnauthorized},
	"google_email_not_verified":       {Code: "google_email_not_verified", Message: "Email Google nao verificado.", Status: http.StatusForbidden},
	"google_user_save_failed":         {Code: "google_user_save_failed", Message: "Falha ao salvar usuario Google.", Status: http.StatusInternalServerError},
	"profile_too_large":               {Code: "profile_too_large", Message: "Dados do perfil excedem o limite permitido.", Status: http.StatusBadRequest},
	"invalid_birth_date":              {Code: "invalid_birth_date", Message: "Data de nascimento invalida.", Status: http.StatusBadRequest},
	"profile_save_failed":             {Code: "profile_save_failed", Message: "Falha ao salvar perfil.", Status: http.StatusInternalServerError},
	"visibility_update_failed":        {Code: "visibility_update_failed", Message: "Falha ao atualizar visibilidade.", Status: http.StatusInternalServerError},
	"profile_incomplete":              {Code: "profile_incomplete", Message: "Complete o perfil antes de ativar visibilidade.", Status: http.StatusBadRequest},
	"profile_photos_list_failed":      {Code: "profile_photos_list_failed", Message: "Falha ao listar fotos do perfil.", Status: http.StatusInternalServerError},
	"invalid_profile_photo_position":  {Code: "invalid_profile_photo_position", Message: "A posicao da foto deve estar entre 0 e 3.", Status: http.StatusBadRequest},
	"invalid_multipart_photo":         {Code: "invalid_multipart_photo", Message: "Upload multipart invalido.", Status: http.StatusBadRequest},
	"missing_photo_file":              {Code: "missing_photo_file", Message: "Arquivo da foto ausente.", Status: http.StatusBadRequest},
	"photo_read_failed":               {Code: "photo_read_failed", Message: "Falha ao ler foto enviada.", Status: http.StatusBadRequest},
	"profile_photo_too_large":         {Code: "profile_photo_too_large", Message: "Foto excede o tamanho maximo permitido.", Status: http.StatusRequestEntityTooLarge},
	"unsupported_profile_photo_type":  {Code: "unsupported_profile_photo_type", Message: "Formato de foto nao suportado.", Status: http.StatusBadRequest},
	"profile_photo_name_failed":       {Code: "profile_photo_name_failed", Message: "Falha ao gerar nome da foto.", Status: http.StatusInternalServerError},
	"profile_photo_upload_failed":     {Code: "profile_photo_upload_failed", Message: "Falha ao enviar foto para o storage.", Status: http.StatusInternalServerError},
	"profile_photo_save_failed":       {Code: "profile_photo_save_failed", Message: "Falha ao salvar foto do perfil.", Status: http.StatusInternalServerError},
	"profile_photo_not_found":         {Code: "profile_photo_not_found", Message: "Foto do perfil nao encontrada.", Status: http.StatusNotFound},
	"profile_photo_delete_failed":     {Code: "profile_photo_delete_failed", Message: "Falha ao remover foto do perfil.", Status: http.StatusInternalServerError},
	"discovery_failed":                {Code: "discovery_failed", Message: "Falha ao carregar perfis.", Status: http.StatusInternalServerError},
	"discovery_photos_failed":         {Code: "discovery_photos_failed", Message: "Falha ao carregar fotos dos perfis.", Status: http.StatusInternalServerError},
	"invalid_swipe":                   {Code: "invalid_swipe", Message: "Acao de swipe invalida.", Status: http.StatusBadRequest},
	"swipe_already_exists":            {Code: "swipe_already_exists", Message: "Swipe ja registrado para esse usuario.", Status: http.StatusConflict},
	"swipe_failed":                    {Code: "swipe_failed", Message: "Falha ao registrar swipe.", Status: http.StatusInternalServerError},
	"matches_failed":                  {Code: "matches_failed", Message: "Falha ao listar matches.", Status: http.StatusInternalServerError},
	"conversations_failed":            {Code: "conversations_failed", Message: "Falha ao listar conversas.", Status: http.StatusInternalServerError},
	"not_conversation_member":         {Code: "not_conversation_member", Message: "Usuario nao participa desta conversa.", Status: http.StatusForbidden},
	"messages_failed":                 {Code: "messages_failed", Message: "Falha ao listar mensagens.", Status: http.StatusInternalServerError},
	"conversation_id_mismatch":        {Code: "conversation_id_mismatch", Message: "Conversa da rota e do corpo nao conferem.", Status: http.StatusBadRequest},
	"invalid_chat_payload":            {Code: "invalid_chat_payload", Message: "Mensagem de chat invalida.", Status: http.StatusBadRequest},
	"chat_payload_text_only":          {Code: "chat_payload_text_only", Message: "Chat aceita somente texto, sem anexos ou campos extras.", Status: http.StatusBadRequest},
	"links_and_media_are_not_allowed": {Code: "links_and_media_are_not_allowed", Message: "Links e midias nao sao permitidos no chat.", Status: http.StatusBadRequest},
	"message_create_failed":           {Code: "message_create_failed", Message: "Falha ao criar mensagem.", Status: http.StatusInternalServerError},
	"missing_session":                 {Code: "missing_session", Message: "Sessao ausente.", Status: http.StatusUnauthorized},
	"invalid_session":                 {Code: "invalid_session", Message: "Sessao invalida ou expirada.", Status: http.StatusUnauthorized},
	"session_lookup_failed":           {Code: "session_lookup_failed", Message: "Falha ao validar sessao.", Status: http.StatusInternalServerError},
	"invalid_uuid":                    {Code: "invalid_uuid", Message: "Identificador invalido.", Status: http.StatusBadRequest},
	"internal_error":                  {Code: "internal_error", Message: "Erro interno.", Status: http.StatusInternalServerError},
}

func Lookup(code string) Definition {
	def, ok := definitions[code]
	if ok {
		return def
	}
	return definitions["internal_error"]
}
