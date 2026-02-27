load 'test_helper/bats-support/load'
load 'test_helper/bats-assert/load'

export xmc="$XMC_BINARY -u artemis -p artemis"

@test "publish message succeeds" {
    run $xmc publish test-topic "HelloPubSub"
    assert_success
}

@test "publish with content-type and message-id flags" {
    run $xmc publish test-topic "test message" -T "text/plain" -I "msg-001"
    assert_success
}

@test "subscribe with timeout exits cleanly" {
    run $xmc subscribe test-topic -t 1
    # exit 0 (message received from previous publish) or exit 0 (timeout) are both acceptable
    [ "$status" -eq 0 ] || [ "$status" -eq 1 ]
}
