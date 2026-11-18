# Russian glossary

One Russian rendering per term, for the console catalog (`messages.po` in this directory) and the Russian mail templates (`internal/infra/out/mail/templates/ru`). The reader is an engineer who also uses the English API, SDKs and docs, so a string says what that engineer would say to a colleague. A term is in one of three kinds, decided here once and not per string:

- **Kept in Latin**: abbreviations, protocol words and names that appear verbatim in the API, the SDKs or a provider's own screen. Never declined, never transliterated.
- **Borrowed**: the Russian engineers' word, written in Cyrillic and declined normally.
- **Translated**: where Russian has an established word that an engineer actually uses.

A literal translation that nobody says is the thing to avoid; not translating is not a goal either. A term that is not in the tables is added here first, in the change that needs it. `retiredWords` in `src/locales/ru/catalog.test.ts` and in the mail package's `russian_glossary_test.go` fail on the words these tables replace.

## Kept in Latin

| Term                                                                            | Russian                                                                          | Note                                                    |
| ------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- | ------------------------------------------------------- |
| SDK, API, SSO, SAML, OIDC, SCIM, SMTP, CORS, URL, ID, JSON, YAML, CSV, UTC      | as is                                                                            | `ID` everywhere, never «идентификатор»                  |
| SRM (sample-ratio mismatch)                                                     | **SRM**; the first mention on a screen: «SRM (доли выборки расходятся с весами)» |                                                         |
| kill switch                                                                     | **kill switch**, lower case, not declined: «Включить kill switch»                |                                                         |
| rollout                                                                         | **rollout**: «Есть rollout», «rollout завершён»                                  |                                                         |
| override                                                                        | **override**, plural **overrides**: «Добавить override», «override задан»        |                                                         |
| prerequisite                                                                    | **prerequisite**, plural **prerequisites**: «Добавить prerequisite»              |                                                         |
| ruleset                                                                         | **ruleset**: «Скачать ruleset»                                                   | the name of the file and the format in the SDK protocol |
| Entity ID, ACS URL, SP, IdP, EntityDescriptor, bearer, claim, issuer            | as is                                                                            | what the identity provider's own screen shows           |
| S3, ClickHouse, Parquet, JSON lines                                             | as is                                                                            |                                                         |
| evaluation reason codes (`fallthrough`, `rule_match`, `targeting_match`, `off`) | as is, in mono                                                                   | they are API values                                     |

## Borrowed

| Term                                                      | Russian                                                 | Not                                   |
| --------------------------------------------------------- | ------------------------------------------------------- | ------------------------------------- |
| instance                                                  | **инстанс** (m.)                                        | экземпляр                             |
| unit, unit type                                           | **юнит**, **тип юнита**                                 | единица (kept only as a unit of time) |
| bucket (hash and S3)                                      | **бакет**                                               | корзина                               |
| session                                                   | **сессия**                                              | сеанс                                 |
| maintainer (role)                                         | **мейнтейнер**                                          | сопровождающий                        |
| webhook, token, tag, hash, alias, endpoint, tenant, alert | вебхук, токен, тег, хеш, алиас, эндпоинт, тенант, алерт | «точка …» for endpoint                |
| targeting                                                 | таргетинг                                               |                                       |
| rotate, rotation (key, secret)                            | **ротировать**, **ротация**                             | заменить, сменить                     |
| holdout                                                   | холдаут                                                 |                                       |

## Translated

| Term                                                                                 | Russian                                                                               | Not                                                 |
| ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------- | --------------------------------------------------- |
| project, environment, flag, segment, rule, experiment, attribute, context, key, salt | проект, окружение, флаг, сегмент, правило, эксперимент, атрибут, контекст, ключ, соль |                                                     |
| variation                                                                            | **вариант**                                                                           | вариация                                            |
| exposure                                                                             | **показ**; to be exposed: «увидеть вариант», «получить показ»                         | экспозиция                                          |
| assignment                                                                           | **назначение**                                                                        | распределение                                       |
| fallthrough (a noun in prose and labels)                                             | **остальным**; «вариант для остальных»                                                | без совпадений. The reason code stays `fallthrough` |
| off variation                                                                        | вариант при выключении                                                                |                                                     |
| evaluation, evaluate                                                                 | вычисление, вычислить                                                                 |                                                     |
| remote evaluation                                                                    | **вычисление на сервере**                                                             | удалённое вычисление (reads as "deleted")           |
| serve                                                                                | отдавать                                                                              | выдавать                                            |
| traffic allocation                                                                   | охват                                                                                 | доля трафика                                        |
| inclusion, included (experiment)                                                     | **попадание в эксперимент**, попал                                                    | включение (collides with on and off)                |
| sink                                                                                 | **приёмник**                                                                          | приёмник данных                                     |
| target (of a sink or webhook)                                                        | получатель                                                                            | цель, целевая система                               |
| batch                                                                                | **пакет**                                                                             | пачка                                               |
| dead letter                                                                          | недоставленный пакет                                                                  |                                                     |
| replay / redeliver                                                                   | повторить отправку / доставить повторно                                               |                                                     |
| retention                                                                            | **срок хранения**                                                                     | ретеншен, хранение                                  |
| gap                                                                                  | пропуск                                                                               |                                                     |
| member                                                                               | участник                                                                              |                                                     |
| role, grant                                                                          | роль; выдать, отозвать роль                                                           |                                                     |
| owner, developer, viewer                                                             | владелец, разработчик, наблюдатель                                                    |                                                     |
| entity (audit log)                                                                   | **объект**                                                                            | сущность                                            |
| actor (audit log)                                                                    | **инициатор**                                                                         | автор, which is `author`                            |
| audit log, audit entry                                                               | журнал аудита, запись журнала аудита                                                  |                                                     |
| change request                                                                       | запрос на изменение                                                                   |                                                     |
| approve, reject                                                                      | одобрить, отклонить                                                                   |                                                     |
| rebase (a change request)                                                            | **перенести на текущую версию**                                                       | перебазировать                                      |
| superseded                                                                           | **неактуален**                                                                        | устарел (which is `stale`), вытеснен                |
| stale flag                                                                           | устаревший флаг                                                                       |                                                     |
| revert                                                                               | **откатить**                                                                          | вернуть                                             |
| protected environment                                                                | защищённое окружение                                                                  |                                                     |
| auth provider                                                                        | **провайдер входа**                                                                   | провайдер аутентификации                            |
| identity provider                                                                    | провайдер идентификации; IdP in tables and narrow labels                              |                                                     |
| identity (linked) / account                                                          | привязанная учётная запись / аккаунт                                                  |                                                     |
| provisioned (by SCIM)                                                                | заведён через SCIM                                                                    | выдан, созданные                                    |
| group mapping                                                                        | **сопоставление групп**                                                               | соответствие                                        |
| enforced domain                                                                      | домен с обязательным входом                                                           | принудительный домен                                |
| origin (CORS)                                                                        | **источник**, with the Latin once in the label: «Разрешённые источники (origins)»     |                                                     |
| enabled, disabled (an object) / on, off (a flag)                                     | включён, отключён / включён, выключен                                                 |                                                     |
| deactivated (a member)                                                               | деактивирован                                                                         |                                                     |
| email                                                                                | почта; «адрес почты» where the address is meant                                       |                                                     |
| kind / type                                                                          | вид / тип                                                                             |                                                     |
| draft, archive                                                                       | черновик, архив                                                                       |                                                     |

## Rules for a Russian string

1. Terms come from the tables above. A term not in them is added there first.
2. Sentence case. Buttons, menu items and tabs are infinitives («Создать», «Повторить отправку»), never nouns («Экспорт» as a button becomes «Экспортировать»).
3. No gendered brackets. Write around the person: «Участник {name} сохранил…» only when the noun carries the gender; otherwise a passive or a noun («Одобрено: {name}», «Сохранено: {who}, {when}»).
4. A thing is not an actor where English makes it one: not «Ретеншен удалил события» but «События удалены по истечении срока хранения».
5. A placeholder that inserts a name is placed where no case is forced on it: «в окружении {name}», «участнику {name}», after a colon, or in the nominative.
6. A message inserted into another message is written in the case its host needs, and the host says so in a translator comment.
7. «ё» is written. Quotes are «». «вы» is lower case. No full stop on labels, buttons and one-line hints; the full stop follows the English.
8. A Russian label may be longer than the English, but a button or a column header that is more than one and a half times as long gets a shorter wording, not an abbreviation.
9. One English message means one thing. A word used as a button and as a label is two messages with a `context`.
10. The same act has the same sentence: one wording for "This cannot be undone", for an empty filtered list, for "Go to…".
11. A noun kept in Latin is never declined and never given a Russian ending or an apostrophe. It is masculine for agreement («override задан», «этот ruleset», «rollout завершён»). It takes the English plural where the meaning is plural («Overrides», «правила, overrides и prerequisites»), and with a number follows the English rule in every Russian plural branch but `one` («1 override», «2 overrides», «5 overrides»). Where a case must be heard, a Russian noun carries it: «в списке overrides», «при проверке prerequisite». At the start of a label it is capitalized like any word («Rollout», «Kill switch»); inside a sentence it is lower case.
