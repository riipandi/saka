import { Code, ConnectError, type CallOptions } from '@connectrpc/connect'
import { describe, expect, it, vi } from 'vite-plus/test'
import {
  REAUTHENTICATION_HEADER,
  StepUpCancelled,
  createStepUpRunner,
  isProofRefusal,
  reauthOptions
} from '#/libraries/reauth/reauth'

describe('reauth', () => {
  it('names the proof in the per-call options, not in transport state', () => {
    expect(reauthOptions('tok_1')).toEqual({
      headers: { [REAUTHENTICATION_HEADER]: 'tok_1' }
    })
  })

  it('reads the proof refusal as unauthenticated and nothing else', () => {
    expect(isProofRefusal(new ConnectError('spent', Code.Unauthenticated))).toBe(true)
    expect(isProofRefusal(new ConnectError('no', Code.NotFound))).toBe(false)
    expect(isProofRefusal(new Error('plain'))).toBe(false)
  })

  it('spends the challenged proof on the one call it asked for', async () => {
    const challenge = vi.fn<() => Promise<string>>().mockResolvedValue('proof_1')
    const call = vi.fn<(options: CallOptions) => Promise<string>>().mockResolvedValue('done')
    const result = await createStepUpRunner(challenge)(call)
    expect(result).toBe('done')
    expect(challenge).toHaveBeenCalledTimes(1)
    expect(call).toHaveBeenCalledWith({ headers: { [REAUTHENTICATION_HEADER]: 'proof_1' } })
  })

  it('challenges once more when the spent proof answers its refusal', async () => {
    const challenge = vi
      .fn<() => Promise<string>>()
      .mockResolvedValueOnce('stale')
      .mockResolvedValueOnce('fresh')
    const call = vi
      .fn<(options: CallOptions) => Promise<string>>()
      .mockRejectedValueOnce(new ConnectError('spent', Code.Unauthenticated))
      .mockResolvedValueOnce('done')
    const result = await createStepUpRunner(challenge)(call)
    expect(result).toBe('done')
    expect(challenge).toHaveBeenCalledTimes(2)
    expect(call).toHaveBeenLastCalledWith({ headers: { [REAUTHENTICATION_HEADER]: 'fresh' } })
  })

  it('retries only the proof refusal — any other failure reaches the caller', async () => {
    const challenge = vi.fn<() => Promise<string>>().mockResolvedValue('proof_1')
    const failure = new ConnectError('gone', Code.NotFound)
    const call = vi.fn<(options: CallOptions) => Promise<string>>().mockRejectedValue(failure)
    await expect(createStepUpRunner(challenge)(call)).rejects.toBe(failure)
    expect(challenge).toHaveBeenCalledTimes(1)
  })

  it('rejects with the dismissal, and the guarded action never runs', async () => {
    const challenge = vi.fn<() => Promise<string>>().mockRejectedValue(new StepUpCancelled())
    const call = vi.fn<(options: CallOptions) => Promise<string>>()
    await expect(createStepUpRunner(challenge)(call)).rejects.toThrow(
      'Reauthentication was cancelled.'
    )
    expect(call).not.toHaveBeenCalled()
  })
})
