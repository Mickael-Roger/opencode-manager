import type { UserQuestionAnswer, UserQuestionRequest } from "../../dsh/types"

export function encodeQuestionAnswers(request: UserQuestionRequest, selected: Readonly<Record<string, readonly string[]>>, custom: Readonly<Record<string, string>>): UserQuestionAnswer[] {
  return request.questions.map(question => ({
    id: question.id,
    selected: selected[question.id] ?? [],
    ...(custom[question.id] ? { custom: custom[question.id] } : {}),
  }))
}
