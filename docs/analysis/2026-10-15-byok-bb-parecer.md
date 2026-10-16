# Parecer técnico: BYOK com o Banco do Brasil (o merchant conecta a própria conta PJ do BB)

Data: 2026-10-15
Status: **análise; nenhuma linha de código foi escrita ou alterada**
Escopo: CTech Billing (cobrança de faturas e extrato em Finanças), com reuso de `api-commons/byok` (`ctech-go-common`).
Antecedente: [`2026-10-11-byok-inter-parecer.md`](2026-10-11-byok-inter-parecer.md) e o
[ADR 0029](../adr/0029-byok-phase-1.md), que desenharam a interface `byok.Provider` para "mais provedores".

Convenção de rotulagem (a mesma do parecer da Inter):

- **[FATO]**: está escrito numa fonte citada ou no código.
- **[INF]**: inferência a partir de fatos citados; precisa de confirmação antes de virar decisão.
- **[REC]**: recomendação, com justificativa.
- **[ABERTO]**: pergunta que o spec não responde; vai para o teste na conta do dono.

## 0. Fontes

Os três arquivos OpenAPI baixados do Portal Developers BB em 2026-10-15 (fonte primária, não há texto em
página renderizada):

| API | Arquivo | Versão | Servidores |
|---|---|---|---|
| Pix | `OpenAPI_BB_Pix_v2.json` | 2.10.6 (OpenAPI 3.1.0) | prod `https://api-pix.bb.com.br/pix/v2`, homologação `https://api-pix.hm.bb.com.br/pix/v2` |
| Extratos | `OpenAPI_BB_Extratos_v2.json` | 2.0.1 | prod `https://extratos.mtls.api.bb.com.br/v2`, homologação `https://extratos.mtls.api.hm.bb.com.br/v2` |
| Cobranças | `OpenAPI_BB_Cobranças_v2.json` | 3.2.2 | sandbox `https://api.sandbox.bb.com.br/cobrancas/v2`, homologação `https://api.hm.bb.com.br/cobrancas/v2`, prod `https://api.bb.com.br/cobrancas/v2` |

Mais as três páginas de vitrine em `bb.com.br/site/developers/api-{pix,extratos,cobranca}/` (público-alvo e pré-requisitos).
DNS consultado em 2026-10-15. Os arquivos não estão no repositório (são do portal); as versões acima identificam o que foi lido.

## 1. Resumo executivo

**Viabilidade: sim, e o BB encaixa melhor no `byok.Provider` do que eu esperava.** A API Pix do BB segue o padrão do
Banco Central (`cob`, `cobv`, `pix`, devolução, webhook), que é o mesmo modelo que a Inter implementa. O extrato é paginado
e traz a contrapartida. Três diferenças mudam o desenho:

1. **[FATO] Não há como simular pagamento** no ambiente de homologação da API Pix (nenhum endpoint de simulação nos 30
   caminhos do spec). O `SimulatePayment` da Inter não tem equivalente. O teste ponta a ponta depende de pagar um Pix real
   de valor mínimo ou de um recurso do portal que o spec não descreve.
2. **[FATO] Não há listagem nem reenvio de callbacks.** `ListCallbacks` e `RetryCallbacks` ficam sem implementação. A rede
   de segurança contra webhook perdido é o extrato e a consulta de Pix, que já existem na nossa varredura.
3. **[FATO] O pagador vem completo, mas só num endpoint próprio do BB**, `GET /pix-bb`: `pagador{cpf|cnpj, nome}` com
   documento inteiro. O `GET /pix` padrão e o webhook trazem só `infoPagador` (texto livre). Isso é **melhor** que a Inter
   (CPF mascarado), mas exige o escopo `pix-bb.read`.

**Ordem recomendada [REC]:** Pix com vencimento (`cobv`), devolução, webhook, `pix-bb` e extrato, na fase 1; sem payout;
sem Cobrança (boleto). Antes de codar, rodar o roteiro da seção 7 na conta PJ do dono para fechar os [ABERTO].

## 2. Mapa `byok.Provider` para o BB

Interface em `api-commons/byok/provider.go` (v1.26.0). Cada linha diz o que o spec do BB oferece.

| Método do `Provider` | BB | Situação |
|---|---|---|
| `Connect` / `Token` | `POST https://oauth.bb.com.br/oauth/token` (homologação `oauth.hm.bb.com.br`), `client_credentials`, escopos por capacidade (`cobv.read|write`, `pix.read|write`, `pix-bb.read`, `webhook.read|write`, `extrato-info`) | **[FATO]** cobre. Ver seção 3. |
| `CreateDueCharge` | `PUT /cobv/{txid}` (`txid` até 35 caracteres) com `calendario.dataDeVencimento`, `validadeAposVencimento`, `devedor{nome, cpf\|cnpj}` (obrigatórios), `valor.original`, `valor.multa`, `valor.juros`, `valor.abatimento`, `valor.desconto`, `chave` | **[FATO]** cobre. O txid determinístico por tentativa (D17) cabe. Multa e juros têm os campos que a multa e o juro mensal configurados na conexão usam. |
| `GetCharge` | `GET /cobv/{txid}`: `status` (`ATIVA`, `CONCLUIDA`, `REMOVIDA_PELO_USUARIO_RECEBEDOR`, `REMOVIDA_PELO_PSP`), `pixCopiaECola`, `hyperlink`, `pix[]` (e2eid, valor, horário, `infoPagador`, devoluções) | **[FATO]** cobre pagamento e valor pago. Sem `pagador` aqui. |
| `UpdateCharge` (nota de crédito) | `PATCH /cobv/{txid}` revisa valor; só vale enquanto a cobrança está `ATIVA` | **[FATO]** cobre. Uma cobrança já paga não revisa (erro `CobVOperacaoInvalida`). |
| `CancelCharge` | `PATCH /cobv/{txid}` com `status` | **[INF]** o spec lista `status` no corpo do `PATCH`; o valor aceito para remover precisa de teste (seção 7). |
| `Refund` / `GetRefund` | `PUT` e `GET /pix/{e2eid}/devolucao/{id}` (`valor`, `natureza` `ORIGINAL`/`RETIRADA`, `descricao`) | **[FATO]** cobre. A soma das devoluções não passa do valor do Pix. |
| `StatementPage` | `GET /conta-corrente/agencia/{agencia}/conta/{conta}`; janela de **31 dias**, `numeroPaginaSolicitacao`, `quantidadeRegistroPaginaSolicitacao` de 30 a 120 | **[FATO]** cobre, com paginação e sem scroll (D21). Campos na seção 4. |
| `Balance` | linhas do mesmo extrato com `indicadorTipoLancamento` `SA`/`SD` (saldo atual, saldo disponível) | **[FATO]** existe; **[INF]** `at Date` se obtém lendo o extrato do dia. |
| `GetWebhook` / `RegisterWebhook` / `DeleteWebhook` | `GET/PUT/DELETE /webhook/{chave}`, corpo `{webhookUrl}`; só **um** webhook por chave Pix, produto único (`/pix`) | **[FATO]** cobre. Só o produto Pix é usado na fase 1. |
| `ListCallbacks` / `RetryCallbacks` | nada equivalente | **[FATO]** **sem suporte.** Precisam virar opcionais (`ErrUnsupported`). |
| `SimulatePayment` | nada equivalente | **[FATO]** **sem suporte.** Ver seção 6. |
| `ParseCallback` | `POST {webhookUrl}/pix`, corpo `{"pix":[{endToEndId, txid, valor, horario, infoPagador, devolucoes[]}]}`, só para Pix com `txid` | **[FATO]** igual ao padrão do Banco Central e ao da Inter. O parser da Inter reaproveita-se quase inteiro. |
| `Payouts` (opcional) | nada: `pix.write` serve a devolução | **[FATO]** o BB não oferece pagamento de chave Pix nestes três specs. Fora de escopo, como já era para merchants. |

## 3. Credenciais e onboarding

- **[FATO]** Três camadas: `OAuth2` `client_credentials`, `mutualTLS` ("exigência do Bacen") e a **chave da aplicação**
  `gw-dev-app-key` enviada como **parâmetro de query** em toda chamada (`developer_application_key`, "item Credenciais" da
  aplicação no portal).
- **[FATO]** Pré-requisitos (páginas de vitrine): cliente BB **pessoa jurídica**, **chave Pix cadastrada e ativa no BB**
  (Pix), conta corrente ativa (Extratos), cadastro no Portal Developers.
- **[INF]** Os campos de credencial do provedor `bb` seriam: `client_id`, `client_secret`, `developer_application_key`,
  certificado e chave privada (mTLS), `agencia` e `conta` (sem dígito) para o extrato, e a `chave` Pix de recebimento. Isso
  muda `CredentialFields()` e o formulário de conexão, que hoje têm a forma da Inter.
- **[ABERTO]** O token endpoint exige mTLS ou só autenticação básica? A aprovação do aplicativo no portal é manual
  (como na Inter, "Em validação") ou imediata? O certificado é do BB, de uma AC ICP-Brasil, ou autoassinado?

## 4. Extrato: o que o merchant ganha e o que falta

**[FATO]** Cada lançamento traz `valorLancamento`, `indicadorSinalLancamento` (`C`/`D`), `dataLancamento`/`dataMovimento`
(`DDMMAAAA`), `codigoHistorico`, `textoDescricaoSubHistorico` (25), `textoInformacaoComplementar` (38), e da contrapartida:
`numeroCadastroPessoaFisicaCadastroNacPessoasJuridicasContrapartida` (15), `indicadorTipoPessoaContrapartida` (`F`/`J`),
`codigoBancoContrapartida`, `numeroISPB`, `codigoAgenciaContrapartida`, `numeroContaContrapartida`, e
`textoIdentificadorUnicoTransacao` (64).

- **[INF]** `textoIdentificadorUnicoTransacao` é provavelmente o `endToEndId` do Pix (32 caracteres no padrão do Banco
  Central, cabe em 64). Se for, o vínculo extrato-cobrança (D20, `byok_linker`) funciona como na Inter. Se não for, o vínculo
  cai para valor, data e documento da contrapartida (mais fraco).
- **[FATO]** O extrato **não traz o nome** da contrapartida. O nome vem de `GET /pix-bb`.
- **[FATO]** Janela máxima de 31 dias por consulta; sem webhook de extrato. A sincronização é por polling, como na Inter.
- **[ABERTO]** Latência: quanto tempo depois de um Pix recebido ele aparece no extrato? O CPF de 15 caracteres vem com zeros
  à esquerda, mascarado ou inteiro? O extrato de homologação tem dados?

## 4a. Pagador completo: `GET /pix-bb`

**[FATO]** `GET /pix-bb` (escopo `pix-bb.read`) aceita `inicio`, `fim`, `txid`, `cpf`, `cnpj`, `chave`,
`devolucaoPresente`, `contestacaoPresente` e devolve, por Pix: `endToEndId`, `txid`, `valor`, `horario`, `infoPagador`,
`pagador{cpf (11), cnpj (14), nome (200)}`, `devolucoes`, `status` (`PAGO`, `EM_CONTESTACAO`, `CONTESTADO`,
`CONTESTACAO_INDEFERIDA`, `DEVOLUCAO_MED`, `BLOQUEADO`, ...) e `integracao`. O filtro `contestacaoPresente` e os status
de MED (mecanismo especial de devolução) mostram que o BB expõe contestação do Pix recebido; a fase 1 não a usa.

**[REC]** Usar `GET /pix-bb?txid=` na confirmação (depois de `GET /cobv` mostrar `CONCLUIDA`) para gravar o pagador
completo uma vez, com `payer_match` calculado contra o documento do devedor (dado do pagador gravado uma vez, spec `2026-10-14-wallet-ledger-billing-rail.md` § 6). **[ABERTO]** Se `pix-bb`
exige habilitação extra além do escopo.

## 5. Rede e infraestrutura

- **[FATO]** Nenhum dos 9 hosts do BB (`api-pix`, `api-pix.hm`, `extratos.mtls.api`, `extratos.mtls.api.hm`, `oauth`,
  `oauth.hm`, `api`, `api.sandbox`, `oauth.sandbox`) tem registro AAAA em 2026-10-15; todos têm A.
- **[REC]** A mesma Lambda `byok-outbound` (IPv4, sem NAT) serve. Não há infraestrutura nova para o egress.
- **[ABERTO]** Se o BB restringe por IP de origem. O spec não cita lista de IPs; a Lambda sai por IPs variáveis. Se houver
  restrição, o desenho do ADR 0029 (D5) precisa de IP fixo, o que muda o custo.

## 6. O que fica mais fraco que na Inter

1. **Sem simulação de pagamento.** A Inter permite pagar uma cobrança no sandbox; o BB não (no Pix). O ciclo "pago" em
   homologação precisa de um Pix real de baixo valor, ou de um recurso do portal fora do spec. **[REC]** Tratar o teste do BB
   como teste de contrato com Pix real de R$ 0,01 a R$ 0,10 na conta do dono, e manter o `SimulatePayment` do BB como
   `ErrUnsupported`; os testes automatizados usam o `byoktest` (fake) como hoje.
2. **Sem reenvio de callbacks.** Um webhook perdido só é recuperado pela varredura de extrato e `pix-bb`. A varredura já é a
   rede de segurança do desenho (`byok_sweep`); o que muda é o intervalo máximo de detecção, que depende do polling.
3. **Um webhook por chave Pix.** O registro é por `chave`; se o merchant já tem webhook de outro sistema na mesma chave, a
   regra D24 (não sobrescrever webhook alheio sem confirmação) se aplica do mesmo jeito.

## 7. Roteiro de teste na conta PJ do dono (fecha os [ABERTO])

Em ordem; cada passo diz o que anotar.

1. **Criar a aplicação** no Portal Developers BB com os escopos da seção 2. Anotar: `client_id`, `client_secret`, `developer_application_key`, como o certificado mTLS é obtido, e se a aprovação é imediata.
2. **Token:** `POST oauth.hm.bb.com.br/oauth/token` (homologação). Anotar: autenticação (básica ou no corpo), se exige mTLS, a validade do token, o limite de pedidos por minuto.
3. **Webhook:** `PUT /webhook/{chave}` apontando para uma URL de teste com mTLS. Anotar: se o BB valida a URL no cadastro, qual certificado cliente ele apresenta ao chamar (a AC para a truststore), o formato exato do corpo, e quantas vezes e por quanto tempo reenvia.
4. **Cobrança:** `PUT /cobv/{txid}` com multa, juros e desconto. Anotar: o `txid` aceito (tamanho e alfabeto), `pixCopiaECola` válido no app do BB, `hyperlink`.
5. **Pagamento:** pagar a cobrança com um Pix real de R$ 0,10 (e verificar se a homologação oferece pagamento pelo portal). Anotar: o tempo até `CONCLUIDA`, o webhook chegou, o corpo.
6. **Pagador:** `GET /pix-bb?txid=...`. Anotar: `pagador` completo, `status`, e se precisa de habilitação além do escopo.
7. **Extrato:** consulta do dia do passo 5. Anotar: latência até aparecer, o que é `textoIdentificadorUnicoTransacao` (comparar com o `endToEndId`), o formato do CPF da contrapartida, paginação, janela.
8. **Revisão e cancelamento:** `PATCH /cobv/{txid}` com novo `valor.original`; depois cancelar. Anotar: o valor de `status` aceito para remover.
9. **Devolução:** `PUT /pix/{e2eid}/devolucao/{id}` de parte do valor. Anotar: prazo de `liquidacao`, e a atualização no extrato.
10. **Erros e limites:** forçar `403`, `404` e `503`, anotar o formato do problema e qualquer cabeçalho de limite.

Os resultados entram numa seção "Verificado em 2026-10-xx" deste parecer, e só então se escreve o spec.

## 8. Recomendação por fases [REC]

- **Fase 0 (este parecer + seção 7):** nenhum código; fecha as dúvidas com a conta do dono.
- **Fase 1:** pacote `api-commons/bb` (par de `api-commons/inter`) com Pix `cobv`, devolução, webhook (`/pix`), `pix-bb` e
  extrato; `byok.Provider` com `ListCallbacks`, `RetryCallbacks` e `SimulatePayment` como opcionais; `CredentialFields()` do
  BB; parser de callback do BB; formulário de conexão e mapeamento do extrato; lançamento atrás de uma conexão de teste do
  dono, igual ao piloto da Inter. Custo de infraestrutura: nenhum acréscimo previsto (mesma Lambda).
- **Fase 2 (se houver demanda):** Cobrança (boleto) e Pix do boleto (`gerar-pix`), que exigem convênio de cobrança e outro
  ciclo de vida (baixa, retorno de movimento); e Pix Automático (`rec`, `cobr`), que o spec do BB já expõe.
- **Fora de escopo:** pagamento de chave Pix (payout) pelo BB.

## 9. Riscos

| Risco | Impacto | Mitigação |
|---|---|---|
| Sem simulação de pagamento no BB | testes ponta a ponta caros e manuais | contrato com Pix real de centavos; testes automatizados no `byoktest` |
| Restrição por IP de origem (se existir) | quebra o egress sem IP fixo | descobrir no passo 1 do roteiro antes de qualquer código |
| AC do webhook do BB desconhecida | webhook rejeitado na truststore | passo 3 do roteiro |
| `textoIdentificadorUnicoTransacao` não ser o `endToEndId` | vínculo extrato-cobrança mais fraco | passo 7; fallback por valor, data e documento |
| Mais um provedor para manter | custo contínuo | só a fase 1, atrás de uma interface que já existe |
